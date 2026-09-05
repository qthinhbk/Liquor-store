package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liquor-store/security-api/internal/config"
	"github.com/liquor-store/security-api/internal/notifications"
)

type notifyTestClient struct {
	api   *Server
	http  *http.Client
	base  string
	pool  *pgxpool.Pool
	token string
}

type notificationTestCredentials struct {
	Email     string
	Password  string
	StoreName string
	StoreCode string
}

func newNotificationTestCredentials(label string) notificationTestCredentials {
	suffix := fmt.Sprintf("%d%s", time.Now().UnixNano(), label)
	return notificationTestCredentials{
		Email:     "go-notify-" + suffix + "@example.test",
		Password:  "Notify-only-" + suffix,
		StoreName: "Go Notify Store " + label,
		StoreCode: "go-notify-" + suffix,
	}
}

func newNotifyTestServer(t *testing.T, membersEnabled bool) *notifyTestClient {
	t.Helper()
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run notification database tests")
	}
	disposableURL := os.Getenv("NOTIFICATION_TEST_DATABASE_URL")
	if strings.TrimSpace(disposableURL) == "" {
		t.Skip("NOTIFICATION_TEST_DATABASE_URL must point to an explicitly disposable PostgreSQL database; refusing to run against DATABASE_URL or any shared environment")
	}
	cfg := config.Config{
		JWTAccessSecret: "notification-test-secret-that-is-long-enough",
		JWTAccessTTL:    15 * time.Minute,
		JWTIssuer:       "liquor-store-security-api",
		JWTAudience:     "liquor-store-owner-dashboard",
		RefreshTTLDays:  30,
		WebOrigin:       "http://localhost:5173",
		AIIngestToken:   testAIIngestToken,
	}
	cfg.RegisterEnabled = true
	cfg.MembersEnabled = membersEnabled
	pool, err := pgxpool.New(context.Background(), disposableURL)
	if err != nil {
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(pool.Close)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := New(cfg, pool, logger)
	testServer := httptest.NewServer(api.Handler())
	t.Cleanup(testServer.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &notifyTestClient{http: &http.Client{Jar: jar, Timeout: 30 * time.Second}, base: testServer.URL, pool: pool, api: api}
}

func (c *notifyTestClient) registerNotificationUser(t *testing.T, creds notificationTestCredentials) (string, string) {
	t.Helper()
	var registered sessionResponse
	doJSON(t, c.http, http.MethodPost, c.base+"/api/v1/auth/register", "", map[string]any{
		"email": creds.Email, "password": creds.Password, "displayName": "Go Notify Owner",
		"storeName": creds.StoreName, "storeCode": creds.StoreCode,
	}, http.StatusCreated, &registered)
	if registered.User.ID == "" || len(registered.User.Stores) == 0 || registered.User.Stores[0].StoreID == "" {
		t.Fatalf("register response is missing user or store identifiers: %s", mustMarshalNotification(t, registered))
	}
	storeID := registered.User.Stores[0].StoreID
	// Assign fixture credentials from trusted server configuration, separately
	// from the HTTP endpoint payload under test.
	c.api.config.NotificationCredentialBindings = append(c.api.config.NotificationCredentialBindings,
		notifications.CredentialBinding{StoreID: storeID, Provider: notifications.ProviderTelegram, CredentialRef: "env://TELEGRAM_BOT_TOKEN"},
		notifications.CredentialBinding{StoreID: storeID, Provider: notifications.ProviderTelegram, ProviderAccountRef: "@KetchRetailSecurityBot", CredentialRef: "env://TELEGRAM_BOT_TOKEN"},
		notifications.CredentialBinding{StoreID: storeID, Provider: notifications.ProviderWhatsApp, ProviderAccountRef: "111122223333", CredentialRef: "env://WHATSAPP_ACCESS_TOKEN"},
	)
	return registered.User.ID, storeID
}

func (c *notifyTestClient) login(t *testing.T, email, password string) string {
	t.Helper()
	var loggedIn sessionResponse
	doJSON(t, c.http, http.MethodPost, c.base+"/api/v1/auth/login", "", map[string]any{
		"email": email, "password": password,
	}, http.StatusCreated, &loggedIn)
	return loggedIn.AccessToken
}

func TestNotificationConfigurationFlow(t *testing.T) {
	client := newNotifyTestServer(t, true)

	owner := newNotificationTestCredentials("")
	operator := newNotificationTestCredentials("-op")
	ownerUserID, ownerStoreID := "", ""
	operatorUserID, operatorStoreID := "", ""

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tx, cleanupErr := client.pool.Begin(ctx)
		if cleanupErr != nil {
			t.Errorf("begin cleanup: %v", cleanupErr)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		resolveUserID := func(creds notificationTestCredentials, target *string) {
			if *target != "" {
				return
			}
			var found string
			if err := tx.QueryRow(ctx, `SELECT "id" FROM "users" WHERE "email"=$1`, creds.Email).Scan(&found); err == nil {
				*target = found
			}
		}
		resolveStoreID := func(creds notificationTestCredentials, userID string, target *string) {
			if *target != "" {
				return
			}
			var found string
			if err := tx.QueryRow(ctx, `SELECT "id" FROM "stores" WHERE "code"=$1`, creds.StoreCode).Scan(&found); err == nil {
				*target = found
				return
			}
			if userID != "" {
				const constrainedMembership = `SELECT s."id" FROM "store_memberships" sm JOIN "stores" s ON s."id"=sm."storeId" WHERE sm."userId"=$1 AND s."code"=$2`
				if err := tx.QueryRow(ctx, constrainedMembership, userID, creds.StoreCode).Scan(&found); err == nil {
					*target = found
				}
			}
		}
		resolveUserID(owner, &ownerUserID)
		resolveStoreID(owner, ownerUserID, &ownerStoreID)
		resolveUserID(operator, &operatorUserID)
		resolveStoreID(operator, operatorUserID, &operatorStoreID)

		storeTargets := []string{}
		seenStores := map[string]bool{}
		for _, id := range []string{ownerStoreID, operatorStoreID} {
			if id != "" && !seenStores[id] {
				seenStores[id] = true
				storeTargets = append(storeTargets, id)
			}
		}
		userTargets := []string{}
		seenUsers := map[string]bool{}
		for _, id := range []string{ownerUserID, operatorUserID} {
			if id != "" && !seenUsers[id] {
				seenUsers[id] = true
				userTargets = append(userTargets, id)
			}
		}
		if len(storeTargets) > 0 {
			if _, cleanupErr = tx.Exec(ctx, `DELETE FROM "notification_deliveries" WHERE "storeId" = ANY($1::uuid[])`, storeTargets); cleanupErr != nil {
				abortCleanup(t, cleanupErr)
				return
			}
			if _, cleanupErr = tx.Exec(ctx, `DELETE FROM "alerts" WHERE "storeId" = ANY($1::uuid[])`, storeTargets); cleanupErr != nil {
				abortCleanup(t, cleanupErr)
				return
			}
		}
		if len(storeTargets) > 0 {
			if _, cleanupErr = tx.Exec(ctx, `DELETE FROM "stores" WHERE "id" = ANY($1::uuid[])`, storeTargets); cleanupErr != nil {
				abortCleanup(t, cleanupErr)
				return
			}
		}
		if len(userTargets) > 0 {
			if _, cleanupErr = tx.Exec(ctx, `DELETE FROM "users" WHERE "id" = ANY($1::uuid[])`, userTargets); cleanupErr != nil {
				abortCleanup(t, cleanupErr)
				return
			}
		}
		if cleanupErr = tx.Commit(ctx); cleanupErr != nil {
			abortCleanup(t, cleanupErr)
		}
	})

	ownerUserID, ownerStoreID = client.registerNotificationUser(t, owner)
	client.token = client.login(t, owner.Email, owner.Password)
	base := client.base

	telegramChat := "5500" + fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	var endpoint notificationEndpointResponse
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", client.token, map[string]any{
		"provider": "TELEGRAM", "label": "Owner Telegram", "providerAccountRef": "@KetchRetailSecurityBot",
		"destinationRef": telegramChat, "credentialRef": "env://TELEGRAM_BOT_TOKEN",
		"config": map[string]any{"parseMode": "HTML"}, "isEnabled": true,
	}, http.StatusCreated, &endpoint)
	assertNoSecretLeak(t, endpoint, telegramChat)

	doJSON(t, client.http, http.MethodGet, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", client.token, nil, http.StatusOK, &[]notificationEndpointResponse{})
	doJSON(t, client.http, http.MethodPatch, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+endpoint.ID, client.token, map[string]any{
		"label": "Owner Telegram Renamed",
	}, http.StatusOK, &notificationEndpointResponse{})

	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", client.token, map[string]any{
		"provider": "TELEGRAM", "label": "Duplicate create", "destinationRef": telegramChat, "credentialRef": "env://TELEGRAM_BOT_TOKEN",
	}, http.StatusConflict, &map[string]any{})
	rawTelegramToken := "123456789:" + strings.Repeat("A", 35)
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", client.token, map[string]any{
		"provider": "TELEGRAM", "label": "Raw token", "destinationRef": "5500000000", "credentialRef": rawTelegramToken,
	}, http.StatusBadRequest, &map[string]any{})

	conflictChat := "7700" + fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	var conflictEndpoint notificationEndpointResponse
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", client.token, map[string]any{
		"provider": "TELEGRAM", "label": "Patch conflict target", "destinationRef": conflictChat, "credentialRef": "env://TELEGRAM_BOT_TOKEN",
	}, http.StatusCreated, &conflictEndpoint)
	doJSON(t, client.http, http.MethodPatch, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+conflictEndpoint.ID, client.token, map[string]any{
		"destinationRef": telegramChat,
	}, http.StatusConflict, &map[string]any{})
	if !endpointUnchanged(t, client, ownerStoreID, conflictEndpoint.ID, conflictChat, "Patch conflict target") {
		t.Fatal("second endpoint was mutated by a conflicting PATCH")
	}

	disposableEndpoint := client.createEndpoint(t, ownerStoreID, "Disposable Telegram", "5500999999")
	doJSON(t, client.http, http.MethodDelete, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+disposableEndpoint, client.token, nil, http.StatusOK, &map[string]bool{})

	var rule notificationRuleResponse
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-rules", client.token, map[string]any{
		"name": "Emergency alerts",
	}, http.StatusCreated, &rule)
	if rule.MinimumSeverity != "CRITICAL" || len(rule.AlertTypes) != 0 || rule.CooldownSeconds != 0 || !rule.IsEnabled {
		t.Fatalf("rule create defaults wrong: %+v", rule)
	}

	channelsPath := base + "/api/v1/stores/" + ownerStoreID + "/notification-rules/" + rule.ID + "/channels"
	var primaryChannel notificationRuleChannelResponse
	doJSON(t, client.http, http.MethodPost, channelsPath, client.token, map[string]any{
		"endpointId": endpoint.ID, "priority": 1, "fallbackDelaySeconds": 120, "isEnabled": true,
	}, http.StatusCreated, &primaryChannel)

	whatsAppConfig := map[string]any{
		"wabaId": "111122223333", "templateName": "emergency_security_alert", "templateLanguage": "en",
		"templateVersion": "whatsapp-emergency-security-alert-v1",
		"optIn":           map[string]any{"capturedAt": "2026-08-24T00:00:00Z", "source": "OWNER_DASHBOARD", "policyVersion": "whatsapp-emergency-alerts-v1"},
	}
	var whatsappEndpoint notificationEndpointResponse
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", client.token, map[string]any{
		"provider": "WHATSAPP", "label": "Owner WhatsApp", "providerAccountRef": "111122223333",
		"destinationRef": "+15550001234", "credentialRef": "env://WHATSAPP_ACCESS_TOKEN", "isEnabled": false,
		"config": whatsAppConfig,
	}, http.StatusCreated, &whatsappEndpoint)
	testPathWhatsApp := base + "/api/v1/stores/" + ownerStoreID + "/notification-endpoints/" + whatsappEndpoint.ID + "/test"
	doJSON(t, client.http, http.MethodPost, testPathWhatsApp, client.token, map[string]any{"requestId": uuid.NewString()}, http.StatusConflict, &map[string]any{})
	doJSON(t, client.http, http.MethodPatch, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+whatsappEndpoint.ID, client.token, map[string]any{
		"isEnabled": true,
	}, http.StatusOK, &notificationEndpointResponse{})

	var whatsappChannel notificationRuleChannelResponse
	doJSON(t, client.http, http.MethodPost, channelsPath, client.token, map[string]any{
		"endpointId": whatsappEndpoint.ID, "priority": 2, "fallbackDelaySeconds": 300, "isEnabled": true,
	}, http.StatusCreated, &whatsappChannel)
	doJSON(t, client.http, http.MethodPost, channelsPath, client.token, map[string]any{
		"endpointId": whatsappEndpoint.ID, "priority": 3,
	}, http.StatusConflict, &map[string]any{})
	doJSON(t, client.http, http.MethodPost, channelsPath, client.token, map[string]any{
		"endpointId": endpoint.ID, "priority": 5,
	}, http.StatusConflict, &map[string]any{})
	doJSON(t, client.http, http.MethodPatch, channelsPath+"/"+whatsappChannel.ID, client.token, map[string]any{
		"priority": 1,
	}, http.StatusConflict, &map[string]any{})
	var channelsAfterConflict []notificationRuleChannelResponse
	doJSON(t, client.http, http.MethodGet, channelsPath, client.token, nil, http.StatusOK, &channelsAfterConflict)
	for _, channel := range channelsAfterConflict {
		expectedPriority := 1
		if channel.EndpointID == whatsappEndpoint.ID {
			expectedPriority = 2
		}
		if channel.Priority != expectedPriority {
			t.Fatalf("channel %s mutated by conflicting PATCH: priority %d, want %d", channel.ID, channel.Priority, expectedPriority)
		}
	}
	doJSON(t, client.http, http.MethodPatch, channelsPath+"/"+whatsappChannel.ID, client.token, map[string]any{
		"fallbackDelaySeconds": 45,
	}, http.StatusOK, &notificationRuleChannelResponse{})
	var listedChannels []notificationRuleChannelResponse
	doJSON(t, client.http, http.MethodGet, channelsPath, client.token, nil, http.StatusOK, &listedChannels)
	if len(listedChannels) != 2 || listedChannels[0].Priority > listedChannels[1].Priority {
		t.Fatalf("channels must be listed by ascending priority, got %+v", listedChannels)
	}

	requestID := uuid.NewString()
	testPath := base + "/api/v1/stores/" + ownerStoreID + "/notification-endpoints/" + endpoint.ID + "/test"
	var firstTest, repeatTest map[string]string
	doJSON(t, client.http, http.MethodPost, testPath, client.token, map[string]any{"requestId": requestID}, http.StatusAccepted, &firstTest)
	doJSON(t, client.http, http.MethodPost, testPath, client.token, map[string]any{"requestId": requestID}, http.StatusAccepted, &repeatTest)
	if firstTest["id"] == "" || firstTest["id"] != repeatTest["id"] || firstTest["deliveryKind"] != "TEST" {
		t.Fatalf("test enqueue not idempotent: %+v vs %+v", firstTest, repeatTest)
	}
	var storedEndpointID, storedKind string
	if err := client.pool.QueryRow(context.Background(), `SELECT "endpointId"::text,"deliveryKind"::text FROM "notification_deliveries" WHERE "id"=$1`, firstTest["id"]).Scan(&storedEndpointID, &storedKind); err != nil {
		t.Fatal(err)
	}
	if storedEndpointID != endpoint.ID || storedKind != "TEST" {
		t.Fatalf("TEST delivery not pinned to endpoint: endpointId=%s kind=%s", storedEndpointID, storedKind)
	}

	ctx := context.Background()
	insertAlert := func(alertID, correlationID string) {
		t.Helper()
		sourceEvent := "task3-" + alertID
		if _, err := client.pool.Exec(ctx, `INSERT INTO "alerts" ("id","sourceEventId","correlationId","storeId","type","severity","detectedAt","updatedAt") VALUES ($1,$2,$3,$4,'WEAPON_DETECTED','CRITICAL',NOW(),NOW())`, alertID, sourceEvent, correlationID, ownerStoreID); err != nil {
			t.Fatal(err)
		}
	}
	newAlertInput := func(alertID, correlationID string) notifications.AlertNotificationInput {
		return notifications.AlertNotificationInput{
			StoreID: ownerStoreID, StoreName: "Go Notify Store", StoreTimezone: "America/Chicago",
			AlertID: alertID, CorrelationID: correlationID, AlertType: "WEAPON_DETECTED",
			Severity: "CRITICAL", DetectedAt: time.Now().UTC(), CameraName: "Whole store",
		}
	}

	alertA := uuid.NewString()
	incidentCorrelation := "corr-" + alertA
	insertAlert(alertA, incidentCorrelation)
	summaries := client.enqueueAlert(t, newAlertInput(alertA, incidentCorrelation))
	if len(summaries) != 2 {
		t.Fatalf("expected PENDING + WAITING_FALLBACK deliveries for the first observation, got %+v", summaries)
	}
	if summaries[0].Status != notifications.StatusPending || summaries[0].Priority != 1 || summaries[1].Status != notifications.StatusWaitingFallback || summaries[1].Priority != 2 {
		t.Fatalf("unexpected route statuses: %+v", summaries)
	}
	var deliveryPage struct {
		Items      []notificationDeliveryResponse `json:"items"`
		NextCursor *string                        `json:"nextCursor"`
	}
	deliveryListPath := base + "/api/v1/stores/" + ownerStoreID + "/notification-deliveries?status=PENDING&provider=TELEGRAM&kind=ALERT&alertId=" + alertA
	doJSON(t, client.http, http.MethodGet, deliveryListPath, client.token, nil, http.StatusOK, &deliveryPage)
	if len(deliveryPage.Items) != 1 || deliveryPage.Items[0].ID != summaries[0].ID || deliveryPage.Items[0].DestinationMasked == telegramChat {
		t.Fatalf("delivery history response is incomplete or exposes the raw destination: %+v", deliveryPage)
	}
	var deliveryDetail struct {
		Delivery notificationDeliveryResponse  `json:"delivery"`
		Attempts []notificationAttemptResponse `json:"attempts"`
	}
	doJSON(t, client.http, http.MethodGet, base+"/api/v1/stores/"+ownerStoreID+"/notification-deliveries/"+summaries[0].ID, client.token, nil, http.StatusOK, &deliveryDetail)
	if deliveryDetail.Delivery.ID != summaries[0].ID || deliveryDetail.Attempts == nil || len(deliveryDetail.Attempts) != 0 {
		t.Fatalf("unexpected initial delivery detail: %+v", deliveryDetail)
	}
	doJSON(t, client.http, http.MethodGet, base+"/api/v1/stores/"+ownerStoreID+"/notification-deliveries?cursor=not-base64", client.token, nil, http.StatusBadRequest, &map[string]any{})

	alertB := uuid.NewString()
	insertAlert(alertB, incidentCorrelation)
	if secondCamera := client.enqueueAlert(t, newAlertInput(alertB, incidentCorrelation)); len(secondCamera) != 0 {
		t.Fatalf("same correlationId from another camera must not add deliveries, got %+v", secondCamera)
	}
	doJSON(t, client.http, http.MethodPatch, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+endpoint.ID, client.token, map[string]any{
		"config": map[string]any{"parseMode": "HTML", "templateVersion": "telegram-emergency-security-alert-v2"},
	}, http.StatusOK, &notificationEndpointResponse{})
	if afterVersionChange := client.enqueueAlert(t, newAlertInput(alertB, incidentCorrelation)); len(afterVersionChange) != 0 {
		t.Fatalf("template version change within one incident must not add deliveries, got %+v", afterVersionChange)
	}

	alertC := uuid.NewString()
	newIncidentCorrelation := "corr-" + alertC
	insertAlert(alertC, newIncidentCorrelation)
	newIncidentSummaries := client.enqueueAlert(t, newAlertInput(alertC, newIncidentCorrelation))
	if len(newIncidentSummaries) != 2 {
		t.Fatalf("a new correlationId must produce a fresh route delivery set, got %+v", newIncidentSummaries)
	}

	doJSON(t, client.http, http.MethodDelete, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+endpoint.ID, client.token, nil, http.StatusConflict, &map[string]any{})
	doJSON(t, client.http, http.MethodDelete, base+"/api/v1/stores/"+ownerStoreID+"/notification-rules/"+rule.ID, client.token, nil, http.StatusConflict, &map[string]any{})
	doJSON(t, client.http, http.MethodDelete, channelsPath+"/"+whatsappChannel.ID, client.token, nil, http.StatusConflict, &map[string]any{})

	operatorUserID, operatorStoreID = client.registerNotificationUser(t, operator)
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/members", client.token, map[string]any{
		"email": operator.Email, "role": "OPERATOR",
	}, http.StatusCreated, &map[string]any{})
	operatorToken := client.login(t, operator.Email, operator.Password)
	doJSON(t, client.http, http.MethodGet, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints", operatorToken, nil, http.StatusForbidden, &map[string]any{})
	doJSON(t, client.http, http.MethodPost, base+"/api/v1/stores/"+ownerStoreID+"/notification-endpoints/"+endpoint.ID+"/test", operatorToken, map[string]any{"requestId": uuid.NewString()}, http.StatusForbidden, &map[string]any{})
	randomStoreID := uuid.NewString()
	doJSON(t, client.http, http.MethodGet, base+"/api/v1/stores/"+randomStoreID+"/notification-endpoints", client.token, nil, http.StatusNotFound, &map[string]any{})
	doJSON(t, client.http, http.MethodPatch, base+"/api/v1/stores/"+randomStoreID+"/notification-rules/"+rule.ID, client.token, map[string]any{"isEnabled": false}, http.StatusNotFound, &map[string]any{})
}

func abortCleanup(t *testing.T, err error) {
	t.Errorf("clean notification data: %v", err)
}

func endpointUnchanged(t *testing.T, client *notifyTestClient, storeID, endpointID, expectedDestination, expectedLabel string) bool {
	t.Helper()
	var destinationRef, label string
	if err := client.pool.QueryRow(context.Background(), `SELECT "destinationRef","label" FROM "notification_endpoints" WHERE "id"=$1 AND "storeId"=$2`, endpointID, storeID).Scan(&destinationRef, &label); err != nil {
		t.Fatal(err)
	}
	return destinationRef == expectedDestination && label == expectedLabel
}

func (c *notifyTestClient) createEndpoint(t *testing.T, storeID, label, chat string) string {
	t.Helper()
	var created notificationEndpointResponse
	doJSON(t, c.http, http.MethodPost, c.base+"/api/v1/stores/"+storeID+"/notification-endpoints", c.token, map[string]any{
		"provider": "TELEGRAM", "label": label, "destinationRef": chat, "credentialRef": "env://TELEGRAM_BOT_TOKEN",
	}, http.StatusCreated, &created)
	return created.ID
}

func (c *notifyTestClient) enqueueAlert(t *testing.T, input notifications.AlertNotificationInput) []notifications.DeliverySummary {
	t.Helper()
	ctx := context.Background()
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := notifications.EnqueueAlertTx(ctx, tx, input)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return summaries
}

func assertNoSecretLeak(t *testing.T, endpoint notificationEndpointResponse, rawDestination string) {
	t.Helper()
	text := strings.ToLower(mustMarshalNotification(t, endpoint))
	for _, forbidden := range []string{"credentialref", "env://", "render-secret://", strings.ToLower(rawDestination), `"payload"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("endpoint response leaks %q: %s", forbidden, text)
		}
	}
	if !endpoint.CredentialConfigured {
		t.Fatal("expected credentialConfigured=true")
	}
}

func mustMarshalNotification(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

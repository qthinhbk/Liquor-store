package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/liquor-store/security-api/internal/config"
	"github.com/liquor-store/security-api/internal/notifications"
)

const (
	productionWhatsAppAlertConfirmation = "SEND_ONE_REAL_STYLE_ALERT"
	productionWhatsAppRetryConfirmation = "RETRY_ONE_AFTER_BILLING_FIX_TO_1585"
)

type productionWhatsAppAlertCandidate struct {
	alertID, deliveryID, ruleID                                         string
	status, templateVersion, accountRef, destination                    string
	credentialRef, evidenceID, storageKey, mimeType                     string
	attemptCount                                                        int
	providerMessageID, providerStatus, providerErrorCode, lastErrorCode *string
	payload, endpointConfig                                             []byte
	endpointEnabled                                                     bool
}

// TestProductionWhatsAppAlertWorkerE2E reuses the WhatsApp fallback belonging
// to the controlled Task 6 alert that already passed Telegram E2E. It does not
// create another alert or another delivery. The explicit second guard is
// required before the cancelled fallback can become eligible for the worker.
func TestProductionWhatsAppAlertWorkerE2E(t *testing.T) {
	if os.Getenv("RUN_PRODUCTION_WHATSAPP_ALERT_E2E") != "1" {
		t.Skip("set RUN_PRODUCTION_WHATSAPP_ALERT_E2E=1 for the read-only production preflight")
	}
	_ = godotenv.Load("../../.env")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect production database: %v", err)
	}
	defer pool.Close()

	candidate, err := loadProductionWhatsAppAlertCandidate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	validateProductionWhatsAppCandidate(t, candidate)
	preflightProductionWhatsAppMeta(t, ctx, candidate)
	preflightProductionEvidence(t, ctx, candidate.storageKey, candidate.mimeType)

	if candidate.status == notifications.StatusSent {
		verifyProductionWhatsAppAlertResult(t, ctx, pool, candidate, candidate.attemptCount)
		waitForProductionEvidenceFetch(t, ctx, pool, candidate.deliveryID)
		waitForProductionWhatsAppReceipt(t, ctx, pool, candidate.deliveryID)
		t.Log("production WhatsApp ALERT was already sent by this guarded test; no additional message was enqueued")
		return
	}
	confirmation := os.Getenv("CONFIRM_PRODUCTION_WHATSAPP_SEND")
	if candidate.status == notifications.StatusFailed {
		if confirmation != productionWhatsAppRetryConfirmation {
			t.Log("the first provider request was rejected with payment eligibility code 131042; billing must be fixed before the explicit one-retry guard is used")
			return
		}
		command, err := pool.Exec(ctx, `UPDATE "notification_deliveries"
SET "status"='PENDING',"maxAttempts"=2,"availableAt"=NOW(),"providerMessageId"=NULL,"providerStatus"=NULL,
    "providerStatusAt"=NULL,"deliveredAt"=NULL,"readAt"=NULL,"providerFailedAt"=NULL,"providerErrorCode"=NULL,
    "sentAt"=NULL,"lastErrorCode"=NULL,"lastErrorMessage"=NULL,"updatedAt"=NOW()
WHERE "id"=$1 AND "status"='FAILED' AND "attemptCount"=1 AND "providerErrorCode"='131042'`, candidate.deliveryID)
		if err != nil {
			t.Fatal(err)
		}
		if command.RowsAffected() != 1 {
			t.Fatal("the failed controlled delivery changed after preflight; no retry was activated")
		}
		candidate = waitForProductionWhatsAppSend(t, ctx, pool, candidate)
		verifyProductionWhatsAppAlertResult(t, ctx, pool, candidate, 2)
		waitForProductionEvidenceFetch(t, ctx, pool, candidate.deliveryID)
		waitForProductionWhatsAppReceipt(t, ctx, pool, candidate.deliveryID)
		t.Log("production WhatsApp E2E passed after one explicitly authorized post-billing retry; no new alert or delivery was created")
		return
	}
	if confirmation != productionWhatsAppAlertConfirmation {
		t.Log("production WhatsApp ALERT preflight passed; no message was sent")
		return
	}

	command, err := pool.Exec(ctx, `UPDATE "notification_deliveries"
SET "status"='PENDING',"availableAt"=NOW(),"lastErrorCode"=NULL,"lastErrorMessage"=NULL,"updatedAt"=NOW()
WHERE "id"=$1 AND "status"='CANCELLED' AND "attemptCount"=0 AND "providerMessageId" IS NULL`, candidate.deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if command.RowsAffected() != 1 {
		t.Fatal("the controlled WhatsApp fallback changed after preflight; no message was sent by this test")
	}

	candidate = waitForProductionWhatsAppSend(t, ctx, pool, candidate)
	verifyProductionWhatsAppAlertResult(t, ctx, pool, candidate, 1)
	waitForProductionEvidenceFetch(t, ctx, pool, candidate.deliveryID)
	waitForProductionWhatsAppReceipt(t, ctx, pool, candidate.deliveryID)
	t.Log("production WhatsApp E2E passed: one real-style ALERT traversed the outbox, worker, provider, secure video link and signed webhook; identifiers were not printed")
}

func loadProductionWhatsAppAlertCandidate(ctx context.Context, pool *pgxpool.Pool) (productionWhatsAppAlertCandidate, error) {
	rows, err := pool.Query(ctx, `SELECT a."id",d."id",d."ruleId",d."status"::text,d."templateVersion",d."attemptCount",
d."providerMessageId",d."providerStatus"::text,d."providerErrorCode",d."lastErrorCode",d."payload",e."providerAccountRef",e."destinationRef",
e."credentialRef",e."config",e."isEnabled",ae."id",ae."storageKey",ae."mimeType"
FROM "alerts" a
JOIN "notification_deliveries" d ON d."alertId"=a."id" AND d."storeId"=a."storeId" AND d."provider"='WHATSAPP' AND d."deliveryKind"='ALERT'
JOIN "notification_endpoints" e ON e."id"=d."endpointId" AND e."storeId"=d."storeId"
JOIN "alert_evidence" ae ON ae."alertId"=a."id" AND ae."id"=NULLIF(d."payload"->>'evidenceId','')::uuid
WHERE a."sourceEventId" LIKE 'task6-production-e2e-%'
  AND a."metadata"->>'controlledTest'='true'
  AND EXISTS (
    SELECT 1 FROM "notification_deliveries" primary_delivery
    WHERE primary_delivery."alertId"=a."id" AND primary_delivery."ruleId"=d."ruleId"
      AND primary_delivery."provider"='TELEGRAM' AND primary_delivery."status"='SENT'
  )
ORDER BY a."createdAt" DESC,d."createdAt" DESC LIMIT 2`)
	if err != nil {
		return productionWhatsAppAlertCandidate{}, err
	}
	defer rows.Close()
	candidates := []productionWhatsAppAlertCandidate{}
	for rows.Next() {
		var item productionWhatsAppAlertCandidate
		if err := rows.Scan(&item.alertID, &item.deliveryID, &item.ruleID, &item.status, &item.templateVersion, &item.attemptCount,
			&item.providerMessageID, &item.providerStatus, &item.providerErrorCode, &item.lastErrorCode, &item.payload, &item.accountRef, &item.destination,
			&item.credentialRef, &item.endpointConfig, &item.endpointEnabled, &item.evidenceID, &item.storageKey, &item.mimeType); err != nil {
			return productionWhatsAppAlertCandidate{}, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return productionWhatsAppAlertCandidate{}, err
	}
	if len(candidates) == 0 {
		return productionWhatsAppAlertCandidate{}, errors.New("no controlled Task 6 alert with an unused WhatsApp fallback was found")
	}
	return candidates[0], nil
}

func validateProductionWhatsAppCandidate(t *testing.T, candidate productionWhatsAppAlertCandidate) {
	t.Helper()
	if candidate.status == notifications.StatusFailed {
		if candidate.attemptCount != 1 || candidate.providerMessageID == nil || candidate.providerStatus == nil || *candidate.providerStatus != "FAILED" || safeProductionCode(candidate.providerErrorCode) != "131042" {
			t.Fatalf("controlled WhatsApp delivery failed in an unexpected state, providerErrorCode=%s", safeProductionCode(candidate.providerErrorCode))
		}
	} else if candidate.status != notifications.StatusCancelled && candidate.status != notifications.StatusSent {
		t.Fatalf("controlled WhatsApp delivery has unsafe status %s", candidate.status)
	}
	if candidate.status == notifications.StatusCancelled && (candidate.attemptCount != 0 || candidate.providerMessageID != nil || candidate.providerStatus != nil) {
		t.Fatal("cancelled WhatsApp fallback already contains provider activity")
	}
	if !candidate.endpointEnabled {
		t.Fatal("production WhatsApp endpoint is disabled")
	}
	if candidate.templateVersion != notifications.WhatsAppLinkedTemplateVersion {
		t.Fatalf("production WhatsApp delivery uses unsupported template snapshot %q", candidate.templateVersion)
	}
	if candidate.credentialRef != "env://WHATSAPP_ACCESS_TOKEN" {
		t.Fatal("production WhatsApp endpoint does not use the supported environment credential reference")
	}
	if !strings.HasSuffix(candidate.destination, "1585") {
		t.Fatal("production WhatsApp destination is not the explicitly approved test recipient")
	}
	if localDestination := strings.TrimSpace(os.Getenv("WHATSAPP_RECIPIENT_PHONE")); localDestination == "" || localDestination != candidate.destination {
		t.Fatal("local WhatsApp recipient does not match the configured production test destination")
	}
	if localAccount := strings.TrimSpace(os.Getenv("WHATSAPP_PHONE_NUMBER_ID")); localAccount == "" || localAccount != candidate.accountRef {
		t.Fatal("local WhatsApp Phone Number ID does not match the production endpoint")
	}
	if err := notifications.ValidateWhatsAppEnableConfig(candidate.accountRef, candidate.destination, candidate.endpointConfig); err != nil {
		t.Fatalf("production WhatsApp endpoint validation failed: %v", err)
	}
	var payload notifications.RenderPayload
	if err := json.Unmarshal(candidate.payload, &payload); err != nil {
		t.Fatal("controlled delivery payload is invalid")
	}
	if payload.Kind != notifications.DeliveryKindAlert || payload.AlertID != candidate.alertID || payload.AlertType != "WEAPON_DETECTED" || payload.EvidenceID != candidate.evidenceID {
		t.Fatal("controlled delivery is not the expected evidence-backed emergency ALERT")
	}
	if payload.DashboardPath != "/#/alerts?alertId="+url.QueryEscape(candidate.alertID) {
		t.Fatal("controlled delivery does not contain the exact dashboard alert deep-link")
	}
}

func preflightProductionWhatsAppMeta(t *testing.T, ctx context.Context, candidate productionWhatsAppAlertCandidate) {
	t.Helper()
	token := strings.TrimSpace(os.Getenv("WHATSAPP_ACCESS_TOKEN"))
	wabaID := strings.TrimSpace(os.Getenv("WHATSAPP_WABA_ID"))
	if token == "" || wabaID == "" {
		t.Fatal("local WhatsApp token and WABA ID are required for read-only Meta preflight")
	}
	var endpointConfig struct {
		WABAID string `json:"wabaId"`
	}
	if err := json.Unmarshal(candidate.endpointConfig, &endpointConfig); err != nil || endpointConfig.WABAID != wabaID {
		t.Fatal("local WABA ID does not match the production endpoint")
	}

	requestURL := notifications.DefaultWhatsAppBaseURL + "/" + url.PathEscape(wabaID) + "/message_templates?name=" + url.QueryEscape(notifications.WhatsAppLinkedTemplateName) + "&fields=name,language,status,category,components"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		t.Fatal("Meta template preflight was unreachable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("Meta template preflight returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Data []struct {
			Name       string `json:"name"`
			Language   string `json:"language"`
			Status     string `json:"status"`
			Category   string `json:"category"`
			Components []struct {
				Type    string `json:"type"`
				Format  string `json:"format"`
				Text    string `json:"text"`
				Buttons []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					URL  string `json:"url"`
				} `json:"buttons"`
			} `json:"components"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal("Meta template preflight returned invalid JSON")
	}
	approved := false
	for _, template := range result.Data {
		if template.Name != notifications.WhatsAppLinkedTemplateName || template.Language != notifications.WhatsAppTemplateLanguage || template.Status != "APPROVED" || template.Category != "UTILITY" {
			continue
		}
		hasVideo, hasBody, hasButton := false, false, false
		for _, component := range template.Components {
			switch component.Type {
			case "HEADER":
				hasVideo = component.Format == "VIDEO"
			case "BODY":
				hasBody = strings.Contains(component.Text, "{{1}}") && strings.Contains(component.Text, "{{2}}") && strings.Contains(component.Text, "{{3}}") && strings.Contains(component.Text, "{{4}}")
			case "BUTTONS":
				for _, button := range component.Buttons {
					if button.Type == "URL" && button.Text == "View alert" && strings.Contains(button.URL, "{{1}}") {
						hasButton = true
					}
				}
			}
		}
		approved = hasVideo && hasBody && hasButton
	}
	if !approved {
		t.Fatal("the exact approved WhatsApp VIDEO/body/View alert template contract was not found")
	}
}

func preflightProductionEvidence(t *testing.T, ctx context.Context, storageKey, mimeType string) {
	t.Helper()
	if mimeType != "video/mp4" {
		t.Fatalf("controlled alert evidence uses unsupported MIME type %q", mimeType)
	}
	origin, err := url.JoinPath("https://ketchenterprise.net", storageKey)
	if err != nil {
		t.Fatal("controlled alert evidence key could not be resolved")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		t.Fatal("controlled alert evidence is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "video/mp4") {
		t.Fatalf("controlled alert evidence preflight returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength <= 0 || response.ContentLength > 16<<20 {
		t.Fatal("controlled alert evidence is empty or exceeds the WhatsApp video limit")
	}
}

func waitForProductionWhatsAppSend(t *testing.T, ctx context.Context, pool *pgxpool.Pool, candidate productionWhatsAppAlertCandidate) productionWhatsAppAlertCandidate {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := pool.QueryRow(ctx, `SELECT "status"::text,"attemptCount","providerMessageId","providerStatus"::text,"lastErrorCode"
FROM "notification_deliveries" WHERE "id"=$1`, candidate.deliveryID).
			Scan(&candidate.status, &candidate.attemptCount, &candidate.providerMessageID, &candidate.providerStatus, &candidate.lastErrorCode)
		if err != nil {
			t.Fatal(err)
		}
		if candidate.status == notifications.StatusFailed {
			t.Fatalf("production WhatsApp worker failed after %d attempt(s), code=%s", candidate.attemptCount, safeProductionCode(candidate.lastErrorCode))
		}
		if candidate.status == notifications.StatusSent && candidate.providerMessageID != nil && strings.TrimSpace(*candidate.providerMessageID) != "" {
			return candidate
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for production WhatsApp worker; status=%s attempts=%d", candidate.status, candidate.attemptCount)
		}
		time.Sleep(2 * time.Second)
	}
}

func verifyProductionWhatsAppAlertResult(t *testing.T, ctx context.Context, pool *pgxpool.Pool, candidate productionWhatsAppAlertCandidate, expectedAttempts int) {
	t.Helper()
	if candidate.status != notifications.StatusSent || candidate.attemptCount != expectedAttempts || candidate.providerMessageID == nil || strings.TrimSpace(*candidate.providerMessageID) == "" {
		t.Fatalf("production WhatsApp ALERT does not have the expected %d accepted provider attempt(s)", expectedAttempts)
	}
	var successfulAttempts, reviewLinks int
	err := pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM "notification_attempts" WHERE "deliveryId"=$1 AND "status"='SUCCEEDED'),
  (SELECT count(*) FROM "notification_video_links" WHERE "deliveryId"=$1)`, candidate.deliveryID).
		Scan(&successfulAttempts, &reviewLinks)
	if err != nil {
		t.Fatal(err)
	}
	if successfulAttempts != expectedAttempts || reviewLinks < expectedAttempts {
		t.Fatalf("incomplete worker audit: successfulAttempts=%d secureLinks=%d", successfulAttempts, reviewLinks)
	}
}

func waitForProductionEvidenceFetch(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deliveryID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var accessedLinks int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM "notification_video_links" WHERE "deliveryId"=$1 AND "accessCount">0`, deliveryID).Scan(&accessedLinks); err != nil {
			t.Fatal(err)
		}
		if accessedLinks > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("provider accepted the message, but did not fetch its secure video within the audit window")
		}
		time.Sleep(2 * time.Second)
	}
}

func waitForProductionWhatsAppReceipt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, deliveryID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var providerStatus *string
		var eventCount int
		err := pool.QueryRow(ctx, `SELECT d."providerStatus"::text,
  (SELECT count(*) FROM "notification_provider_events" e WHERE e."deliveryId"=d."id")
FROM "notification_deliveries" d WHERE d."id"=$1`, deliveryID).Scan(&providerStatus, &eventCount)
		if err != nil {
			t.Fatal(err)
		}
		if providerStatus != nil && eventCount > 0 && (*providerStatus == "SENT" || *providerStatus == "DELIVERED" || *providerStatus == "READ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("provider accepted the message, but no signed WhatsApp receipt reached the production webhook")
		}
		time.Sleep(2 * time.Second)
	}
}

func safeProductionCode(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "none"
	}
	return strings.TrimSpace(*value)
}

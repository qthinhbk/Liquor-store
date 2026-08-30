package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type scriptedSender struct {
	provider Provider
	mu       sync.Mutex
	errors   []error
	hits     int
	lastID   string
}

type concurrencyProbeSender struct {
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	hits    int
}

func (s *concurrencyProbeSender) Provider() Provider { return ProviderTelegram }
func (s *concurrencyProbeSender) Send(ctx context.Context, _ SendRequest) (SendResult, error) {
	s.mu.Lock()
	s.hits++
	messageID := fmt.Sprintf("concurrent-message-%d", s.hits)
	s.mu.Unlock()
	s.started <- struct{}{}
	select {
	case <-s.release:
		return SendResult{ProviderMessageID: messageID, ResponseMetadata: map[string]any{"httpStatus": 200}}, nil
	case <-ctx.Done():
		return SendResult{}, &TransientSendError{Code: TelegramCodeNetworkError}
	}
}

func (s *scriptedSender) Provider() Provider { return s.provider }
func (s *scriptedSender) Send(_ context.Context, _ SendRequest) (SendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	if len(s.errors) > 0 {
		err := s.errors[0]
		s.errors = s.errors[1:]
		return SendResult{}, err
	}
	s.lastID = fmt.Sprintf("%s-message-%d", strings.ToLower(string(s.provider)), s.hits)
	return SendResult{ProviderMessageID: s.lastID, ResponseMetadata: map[string]any{"httpStatus": 200, "secret": "drop-me"}}, nil
}

func (s *scriptedSender) lastMessageID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastID
}
func (s *scriptedSender) queue(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errors = append(s.errors, err)
}

func TestNotificationRuntimePostgreSQL(t *testing.T) {
	if os.Getenv("RUN_NOTIFICATION_RUNTIME_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_NOTIFICATION_RUNTIME_INTEGRATION_TESTS=1 with an explicit disposable database URL")
	}
	databaseURL := strings.TrimSpace(os.Getenv("NOTIFICATION_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NOTIFICATION_TEST_DATABASE_URL is required; DATABASE_URL is intentionally ignored")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	storeID := uuid.NewString()
	ruleID := uuid.NewString()
	telegramEndpointID := uuid.NewString()
	telegramChannelID := uuid.NewString()
	whatsAppEndpointID := uuid.NewString()
	whatsAppChannelID := uuid.NewString()
	fallbackTelegramEndpointID := uuid.NewString()
	fallbackTelegramChannelID := uuid.NewString()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "notification_video_links" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "notification_deliveries" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "alerts" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "notification_rule_channels" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "notification_rules" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "notification_endpoints" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(cleanupCtx, `DELETE FROM "stores" WHERE "id"=$1`, storeID)
	})

	_, err = db.Exec(ctx, `INSERT INTO "stores" ("id","name","code","timezone","updatedAt") VALUES ($1,'Runtime Test',$2,'America/Chicago',NOW())`, storeID, "runtime-"+strings.ToLower(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO "notification_rules" ("id","storeId","name","minimumSeverity","alertTypes","cooldownSeconds","updatedAt") VALUES ($1,$2,'Emergency','CRITICAL',ARRAY['WEAPON_DETECTED']::"AlertType"[],0,NOW())`, ruleID, storeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO "notification_endpoints" ("id","storeId","provider","label","destinationRef","credentialRef","config","updatedAt") VALUES ($1,$2,'TELEGRAM','Owner Telegram','12345','env://TEST_TOKEN','{}',NOW())`, telegramEndpointID, storeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO "notification_rule_channels" ("id","storeId","ruleId","endpointId","priority","fallbackDelaySeconds","updatedAt") VALUES ($1,$2,$3,$4,1,0,NOW())`, telegramChannelID, storeID, ruleID, telegramEndpointID)
	if err != nil {
		t.Fatal(err)
	}

	telegram := &scriptedSender{provider: ProviderTelegram}
	whatsApp := &scriptedSender{provider: ProviderWhatsApp}
	worker := NewWorker(db, slog.New(slog.NewTextHandler(io.Discard, nil)), []Sender{telegram, whatsApp}, nil, WorkerOptions{
		BatchSize: 10, LeaseDuration: 15 * time.Second, BaseBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond,
	})

	alertOne, evidenceOne := insertRuntimeAlert(t, ctx, db, storeID, "retry-incident")
	telegram.queue(&TransientSendError{Code: TelegramCodeProviderUnavailable})
	enqueueRuntimeAlert(t, ctx, db, storeID, alertOne, evidenceOne, "retry-incident")
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertOne, telegramEndpointID, StatusRetryScheduled, 1)
	time.Sleep(5 * time.Millisecond)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertOne, telegramEndpointID, StatusSent, 2)

	_, err = db.Exec(ctx, `INSERT INTO "notification_endpoints" ("id","storeId","provider","label","providerAccountRef","destinationRef","credentialRef","config","updatedAt") VALUES ($1,$2,'WHATSAPP','Owner WhatsApp','111','+15551234567','env://TEST_WA_TOKEN','{}',NOW())`, whatsAppEndpointID, storeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO "notification_rule_channels" ("id","storeId","ruleId","endpointId","priority","fallbackDelaySeconds","updatedAt") VALUES ($1,$2,$3,$4,2,0,NOW())`, whatsAppChannelID, storeID, ruleID, whatsAppEndpointID)
	if err != nil {
		t.Fatal(err)
	}
	alertTwo, evidenceTwo := insertRuntimeAlert(t, ctx, db, storeID, "fallback-incident")
	telegram.queue(&PermanentSendError{Code: TelegramCodeForbidden, Detail: "provider body must not persist"})
	enqueueRuntimeAlert(t, ctx, db, storeID, alertTwo, evidenceTwo, "fallback-incident")
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertTwo, telegramEndpointID, StatusFailed, 1)
	assertDeliveryStatus(t, ctx, db, alertTwo, whatsAppEndpointID, StatusPending, 0)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertTwo, whatsAppEndpointID, StatusSent, 1)
	whatsAppMessageID := whatsApp.lastMessageID()
	if whatsAppMessageID == "" {
		t.Fatal("WhatsApp sender did not return a provider message ID")
	}
	providerEventAt := time.Now().UTC().Truncate(time.Second)
	receiptEvents := []WhatsAppStatusEvent{
		{ProviderMessageID: whatsAppMessageID, Status: ProviderReceiptSent, EventAt: providerEventAt},
		{ProviderMessageID: whatsAppMessageID, Status: ProviderReceiptRead, EventAt: providerEventAt.Add(2 * time.Second)},
		{ProviderMessageID: whatsAppMessageID, Status: ProviderReceiptDelivered, EventAt: providerEventAt.Add(time.Second)},
		{ProviderMessageID: whatsAppMessageID, Status: ProviderReceiptFailed, EventAt: providerEventAt.Add(3 * time.Second), ErrorCode: "131026"},
	}
	appliedReceipts, err := ApplyWhatsAppStatusEvents(ctx, db, receiptEvents)
	if err != nil || appliedReceipts != len(receiptEvents) {
		t.Fatalf("apply WhatsApp receipts = %d, err=%v", appliedReceipts, err)
	}
	duplicateReceipts, err := ApplyWhatsAppStatusEvents(ctx, db, receiptEvents)
	if err != nil || duplicateReceipts != 0 {
		t.Fatalf("duplicate WhatsApp receipts = %d, err=%v", duplicateReceipts, err)
	}
	unknownReceipts, err := ApplyWhatsAppStatusEvents(ctx, db, []WhatsAppStatusEvent{{ProviderMessageID: "unknown-provider-message", Status: ProviderReceiptRead, EventAt: providerEventAt}})
	if err != nil || unknownReceipts != 0 {
		t.Fatalf("unknown WhatsApp receipt = %d, err=%v", unknownReceipts, err)
	}
	var providerStatus string
	var deliveredAt, readAt time.Time
	var providerFailedAt *time.Time
	var providerErrorCode *string
	if err := db.QueryRow(ctx, `SELECT "providerStatus"::text,"deliveredAt","readAt","providerFailedAt","providerErrorCode" FROM "notification_deliveries" WHERE "alertId"=$1 AND "endpointId"=$2`, alertTwo, whatsAppEndpointID).Scan(&providerStatus, &deliveredAt, &readAt, &providerFailedAt, &providerErrorCode); err != nil {
		t.Fatal(err)
	}
	if providerStatus != ProviderReceiptRead || !deliveredAt.Equal(providerEventAt.Add(time.Second)) || !readAt.Equal(providerEventAt.Add(2*time.Second)) || providerFailedAt != nil || providerErrorCode != nil {
		t.Fatalf("receipt summary was downgraded: status=%s delivered=%s read=%s failed=%v code=%v", providerStatus, deliveredAt, readAt, providerFailedAt, providerErrorCode)
	}
	var providerEventCount int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM "notification_provider_events" pe JOIN "notification_deliveries" d ON d."id"=pe."deliveryId" WHERE d."alertId"=$1 AND d."endpointId"=$2`, alertTwo, whatsAppEndpointID).Scan(&providerEventCount); err != nil {
		t.Fatal(err)
	}
	if providerEventCount != len(receiptEvents) {
		t.Fatalf("stored %d provider events, want %d", providerEventCount, len(receiptEvents))
	}

	_, err = db.Exec(ctx, `INSERT INTO "notification_endpoints" ("id","storeId","provider","label","destinationRef","credentialRef","config","updatedAt") VALUES ($1,$2,'TELEGRAM','Backup Telegram','67890','env://TEST_TOKEN','{}',NOW())`, fallbackTelegramEndpointID, storeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO "notification_rule_channels" ("id","storeId","ruleId","endpointId","priority","fallbackDelaySeconds","updatedAt") VALUES ($1,$2,$3,$4,3,0,NOW())`, fallbackTelegramChannelID, storeID, ruleID, fallbackTelegramEndpointID)
	if err != nil {
		t.Fatal(err)
	}
	alertProviderFailure, evidenceProviderFailure := insertRuntimeAlert(t, ctx, db, storeID, "provider-failure-incident")
	telegram.queue(&PermanentSendError{Code: TelegramCodeForbidden})
	enqueueRuntimeAlert(t, ctx, db, storeID, alertProviderFailure, evidenceProviderFailure, "provider-failure-incident")
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, telegramEndpointID, StatusFailed, 1)
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, whatsAppEndpointID, StatusPending, 0)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, whatsAppEndpointID, StatusSent, 1)
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, fallbackTelegramEndpointID, StatusCancelled, 0)
	failedWhatsAppMessageID := whatsApp.lastMessageID()
	appliedFailure, err := ApplyWhatsAppStatusEvents(ctx, db, []WhatsAppStatusEvent{{
		ProviderMessageID: failedWhatsAppMessageID,
		Status:            ProviderReceiptFailed,
		EventAt:           providerEventAt.Add(10 * time.Second),
		ErrorCode:         "131026",
	}})
	if err != nil || appliedFailure != 1 {
		t.Fatalf("apply asynchronous WhatsApp failure = %d, err=%v", appliedFailure, err)
	}
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, whatsAppEndpointID, StatusFailed, 1)
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, fallbackTelegramEndpointID, StatusPending, 0)
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertProviderFailure, fallbackTelegramEndpointID, StatusSent, 1)
	if _, err := db.Exec(ctx, `UPDATE "notification_rule_channels" SET "isEnabled"=false,"updatedAt"=NOW() WHERE "id"=$1`, fallbackTelegramChannelID); err != nil {
		t.Fatal(err)
	}

	alertLease, evidenceLease := insertRuntimeAlert(t, ctx, db, storeID, "lease-incident")
	enqueueRuntimeAlert(t, ctx, db, storeID, alertLease, evidenceLease, "lease-incident")
	staleClaims, err := worker.claim(ctx)
	if err != nil || len(staleClaims) != 1 {
		t.Fatalf("initial lease claim = %d, err=%v", len(staleClaims), err)
	}
	if _, err := db.Exec(ctx, `UPDATE "notification_deliveries" SET "lockedAt"=NOW()-interval '2 minutes',"lockedUntil"=NOW()-interval '1 minute' WHERE "id"=$1`, staleClaims[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := worker.recoverExpiredLeases(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE "notification_deliveries" SET "availableAt"=NOW() WHERE "id"=$1`, staleClaims[0].ID); err != nil {
		t.Fatal(err)
	}
	freshClaims, err := worker.claim(ctx)
	if err != nil || len(freshClaims) != 1 || freshClaims[0].AttemptCount != 2 {
		t.Fatalf("replacement lease claim = %+v, err=%v", freshClaims, err)
	}
	if err := worker.finishSuccess(ctx, staleClaims[0], time.Now().UTC(), SendResult{ProviderMessageID: "stale-result"}); err == nil {
		t.Fatal("an expired worker lease must not overwrite the replacement attempt")
	}
	var leaseStatus string
	var leaseAttempts int
	if err := db.QueryRow(ctx, `SELECT "status"::text,"attemptCount" FROM "notification_deliveries" WHERE "id"=$1`, staleClaims[0].ID).Scan(&leaseStatus, &leaseAttempts); err != nil {
		t.Fatal(err)
	}
	if leaseStatus != StatusProcessing || leaseAttempts != 2 {
		t.Fatalf("stale completion changed replacement lease: status=%s attempts=%d", leaseStatus, leaseAttempts)
	}
	if err := worker.finishSuccess(ctx, freshClaims[0], time.Now().UTC(), SendResult{ProviderMessageID: "fresh-result"}); err != nil {
		t.Fatal(err)
	}
	assertDeliveryStatus(t, ctx, db, alertLease, telegramEndpointID, StatusSent, 2)

	firstConcurrent := enqueueRuntimeTestDelivery(t, ctx, db, storeID, telegramEndpointID, uuid.NewString())
	secondConcurrent := enqueueRuntimeTestDelivery(t, ctx, db, storeID, telegramEndpointID, uuid.NewString())
	probe := &concurrencyProbeSender{started: make(chan struct{}, 2), release: make(chan struct{})}
	concurrentWorker := NewWorker(db, slog.New(slog.NewTextHandler(io.Discard, nil)), []Sender{probe}, nil, WorkerOptions{
		BatchSize: 2, LeaseDuration: 15 * time.Second,
	})
	runDone := make(chan error, 1)
	go func() { runDone <- concurrentWorker.RunOnce(ctx) }()
	for index := 0; index < 2; index++ {
		select {
		case <-probe.started:
		case <-time.After(time.Second):
			close(probe.release)
			<-runDone
			t.Fatal("claimed deliveries were not started concurrently within the lease window")
		}
	}
	close(probe.release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	for _, deliveryID := range []string{firstConcurrent.ID, secondConcurrent.ID} {
		var status string
		if err := db.QueryRow(ctx, `SELECT "status"::text FROM "notification_deliveries" WHERE "id"=$1`, deliveryID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != StatusSent {
			t.Fatalf("concurrent delivery %s status = %s", deliveryID, status)
		}
	}

	alertThree, evidenceThree := insertRuntimeAlert(t, ctx, db, storeID, "dedupe-incident")
	first := enqueueRuntimeAlert(t, ctx, db, storeID, alertThree, evidenceThree, "dedupe-incident")
	if len(first) != 2 {
		t.Fatalf("first correlated enqueue created %d deliveries, want 2", len(first))
	}
	alertFour, evidenceFour := insertRuntimeAlert(t, ctx, db, storeID, "dedupe-incident")
	second := enqueueRuntimeAlert(t, ctx, db, storeID, alertFour, evidenceFour, "dedupe-incident")
	if len(second) != 0 {
		t.Fatalf("duplicate incident enqueue created %d deliveries", len(second))
	}

	var requestedPath, requestedRange string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath, requestedRange = r.URL.EscapedPath(), r.Header.Get("Range")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("demo"))
	}))
	defer origin.Close()
	reviews, err := NewSecureReviewService(db, "https://api.example.test", origin.URL+"/objects", "", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	link, err := reviews.CreateReviewLink(ctx, ReviewLinkInput{StoreID: storeID, AlertID: alertTwo, EvidenceID: evidenceTwo})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(link, "runtime/") || strings.Contains(link, evidenceTwo) {
		t.Fatalf("review URL leaks evidence locator/id: %s", link)
	}
	token := link[strings.LastIndex(link, "/")+1:]
	digest := sha256.Sum256([]byte(token))
	var storedHash []byte
	if err := db.QueryRow(ctx, `SELECT "tokenHash" FROM "notification_video_links" WHERE "alertId"=$1 ORDER BY "createdAt" DESC LIMIT 1`, alertTwo).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) == token || string(storedHash) != string(digest[:]) {
		t.Fatal("database must contain only the SHA-256 token digest")
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/notification-review/"+token, nil)
	request.Header.Set("Range", "bytes=0-3")
	reviews.ServeToken(recorder, request, token)
	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "demo" {
		t.Fatalf("secure proxy response = %d %q", recorder.Code, recorder.Body.String())
	}
	if requestedRange != "bytes=0-3" || requestedPath != "/objects/runtime/"+evidenceTwo+"/clip%2001.mp4" {
		t.Fatalf("origin request path/range = %q %q", requestedPath, requestedRange)
	}
	time.Sleep(2100 * time.Millisecond)
	expired := httptest.NewRecorder()
	reviews.ServeToken(expired, httptest.NewRequest(http.MethodGet, "/", nil), token)
	if expired.Code != http.StatusNotFound {
		t.Fatalf("expired review token returned %d", expired.Code)
	}
}

func insertRuntimeAlert(t *testing.T, ctx context.Context, db *pgxpool.Pool, storeID, correlationID string) (string, string) {
	t.Helper()
	alertID, evidenceID := uuid.NewString(), uuid.NewString()
	_, err := db.Exec(ctx, `INSERT INTO "alerts" ("id","correlationId","storeId","type","severity","status","subjectPersonCategory","detectedAt","updatedAt") VALUES ($1,$2,$3,'WEAPON_DETECTED','CRITICAL','NEW','UNKNOWN',NOW(),NOW())`, alertID, correlationID, storeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO "alert_evidence" ("id","alertId","storageKey","mimeType","durationSeconds","startsAt","endsAt") VALUES ($1,$2,$3,'video/mp4',15,NOW(),NOW()+interval '15 seconds')`, evidenceID, alertID, "runtime/"+evidenceID+"/clip 01.mp4")
	if err != nil {
		t.Fatal(err)
	}
	return alertID, evidenceID
}

func enqueueRuntimeAlert(t *testing.T, ctx context.Context, db *pgxpool.Pool, storeID, alertID, evidenceID, correlationID string) []DeliverySummary {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	items, err := EnqueueAlertTx(ctx, tx, AlertNotificationInput{
		StoreID: storeID, StoreName: "Runtime Test", StoreTimezone: "America/Chicago", AlertID: alertID,
		CorrelationID: correlationID, AlertType: "WEAPON_DETECTED", Severity: "CRITICAL", DetectedAt: time.Now(),
		CameraName: "Whole store", EvidenceID: evidenceID, StorageKey: "must-not-be-persisted",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return items
}

func enqueueRuntimeTestDelivery(t *testing.T, ctx context.Context, db *pgxpool.Pool, storeID, endpointID, requestID string) DeliverySummary {
	t.Helper()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	delivery, err := EnqueueTestTx(ctx, tx, TestDeliveryInput{StoreID: storeID, EndpointID: endpointID, RequestID: requestID})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return delivery
}

func assertDeliveryStatus(t *testing.T, ctx context.Context, db *pgxpool.Pool, alertID, endpointID, wantStatus string, wantAttempts int) {
	t.Helper()
	var status string
	var attemptCount int
	var payload []byte
	err := db.QueryRow(ctx, `SELECT "status"::text,"attemptCount","payload" FROM "notification_deliveries" WHERE "alertId"=$1 AND "endpointId"=$2`, alertID, endpointID).Scan(&status, &attemptCount, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || attemptCount != wantAttempts {
		t.Fatalf("delivery %s/%s = %s attempts=%d; want %s attempts=%d", alertID, endpointID, status, attemptCount, wantStatus, wantAttempts)
	}
	for _, forbidden := range []string{"storageKey", "must-not-be-persisted", "credentialRef", "destinationRef"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("durable payload leaks %q: %s", forbidden, payload)
		}
	}
	var attempts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM "notification_attempts" a JOIN "notification_deliveries" d ON d."id"=a."deliveryId" WHERE d."alertId"=$1 AND d."endpointId"=$2`, alertID, endpointID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != wantAttempts {
		t.Fatalf("immutable attempts = %d, want %d", attempts, wantAttempts)
	}
	var leaked bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "notification_attempts" a JOIN "notification_deliveries" d ON d."id"=a."deliveryId" WHERE d."alertId"=$1 AND (COALESCE(a."errorMessage",'') LIKE '%provider body%' OR a."responseMetadata"::text LIKE '%drop-me%'))`, alertID).Scan(&leaked); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("attempt audit leaked provider detail or unapproved metadata")
	}
}

func TestOpaqueReviewTokenLength(t *testing.T) {
	raw := make([]byte, 32)
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(token) != 43 {
		t.Fatalf("256-bit raw-url token length = %d", len(token))
	}
}

package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	AttemptStatusSucceeded      = "SUCCEEDED"
	AttemptStatusFailed         = "FAILED"
	WorkerCodeSenderUnavailable = "notification_sender_unavailable"
	WorkerCodeEndpointDisabled  = "notification_endpoint_disabled"
	WorkerCodeLeaseExpired      = "notification_worker_lease_expired"
	WorkerCodeUnexpectedError   = "notification_unexpected_error"
	reviewLinkCleanupInterval   = time.Hour
	reviewLinkRetention         = 24 * time.Hour
	maxProviderRetryDelay       = time.Hour
)

type WorkerOptions struct {
	PollInterval  time.Duration
	LeaseDuration time.Duration
	BatchSize     int
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
}

type Worker struct {
	db      *pgxpool.Pool
	log     *slog.Logger
	senders map[Provider]Sender
	links   ReviewLinkBuilder
	options WorkerOptions
	now     func() time.Time

	maintenanceMu         sync.Mutex
	nextReviewLinkCleanup time.Time
}

type claimedDelivery struct {
	ID, Kind, StoreID, EndpointID, Provider, TemplateVersion  string
	AlertID, RuleID, EvidenceID                               *string
	ProviderAccountRef, DestinationRef, CredentialRef         string
	EndpointEnabled                                           bool
	Config                                                    json.RawMessage
	Payload                                                   RenderPayload
	Priority, FallbackDelaySeconds, AttemptCount, MaxAttempts int
}

func NewWorker(db *pgxpool.Pool, logger *slog.Logger, senders []Sender, links ReviewLinkBuilder, options WorkerOptions) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 45 * time.Second
	}
	if options.BatchSize < 1 || options.BatchSize > 100 {
		options.BatchSize = 10
	}
	if options.BaseBackoff <= 0 {
		options.BaseBackoff = 2 * time.Second
	}
	if options.MaxBackoff <= 0 {
		options.MaxBackoff = 5 * time.Minute
	}
	registry := make(map[Provider]Sender, len(senders))
	for _, sender := range senders {
		if sender != nil {
			registry[sender.Provider()] = sender
		}
	}
	return &Worker{db: db, log: logger, senders: registry, links: links, options: options, now: time.Now}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.db == nil {
		return
	}
	ticker := time.NewTicker(w.options.PollInterval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.log.Error("notification worker cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	if w == nil || w.db == nil {
		return errors.New("notification worker database is unavailable")
	}
	if err := w.recoverExpiredLeases(ctx); err != nil {
		return err
	}
	if err := w.cleanupExpiredReviewLinks(ctx); err != nil {
		w.log.Warn("expired notification review link cleanup failed", "errorCode", "notification_review_link_cleanup_failed")
	}
	deliveries, err := w.claim(ctx)
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	workers.Add(len(deliveries))
	for i := range deliveries {
		item := deliveries[i]
		go func() {
			defer workers.Done()
			if err := w.process(ctx, item); err != nil {
				w.log.Error("notification delivery processing failed", "deliveryId", item.ID, "provider", item.Provider, "error", err)
			}
		}()
	}
	workers.Wait()
	return nil
}

func (w *Worker) cleanupExpiredReviewLinks(ctx context.Context) error {
	now := w.now().UTC()
	w.maintenanceMu.Lock()
	if !w.nextReviewLinkCleanup.IsZero() && now.Before(w.nextReviewLinkCleanup) {
		w.maintenanceMu.Unlock()
		return nil
	}
	w.nextReviewLinkCleanup = now.Add(reviewLinkCleanupInterval)
	w.maintenanceMu.Unlock()

	cutoff := now.Add(-reviewLinkRetention)
	_, err := w.db.Exec(ctx, `WITH expired AS (
  SELECT "id" FROM "notification_video_links"
  WHERE "expiresAt"<$1 OR ("revokedAt" IS NOT NULL AND "revokedAt"<$1)
  ORDER BY "expiresAt","id" LIMIT 500
)
DELETE FROM "notification_video_links" l USING expired WHERE l."id"=expired."id"`, cutoff)
	if err != nil {
		w.maintenanceMu.Lock()
		w.nextReviewLinkCleanup = now.Add(5 * time.Minute)
		w.maintenanceMu.Unlock()
	}
	return err
}

func (w *Worker) claim(ctx context.Context) ([]claimedDelivery, error) {
	leaseSeconds := w.options.LeaseDuration.Seconds()
	rows, err := w.db.Query(ctx, `WITH candidates AS (
  SELECT "id" FROM "notification_deliveries"
  WHERE "status" IN ('PENDING','RETRY_SCHEDULED') AND "availableAt"<=NOW() AND "attemptCount"<"maxAttempts"
  ORDER BY "availableAt","createdAt","id" FOR UPDATE SKIP LOCKED LIMIT $1
), claimed AS (
  UPDATE "notification_deliveries" d SET "status"='PROCESSING',"attemptCount"=d."attemptCount"+1,
    "lastAttemptAt"=NOW(),"lockedAt"=NOW(),"lockedUntil"=NOW()+make_interval(secs=>$2::double precision),"updatedAt"=NOW()
  FROM candidates c WHERE d."id"=c."id" RETURNING d.*
)
SELECT d."id",d."deliveryKind"::text,d."storeId",d."alertId",d."ruleId",d."endpointId",d."provider"::text,
 d."priority",d."fallbackDelaySeconds",d."templateVersion",d."payload",d."attemptCount",d."maxAttempts",
 COALESCE(e."providerAccountRef",''),e."destinationRef",e."credentialRef",COALESCE(e."config",'{}'::jsonb),e."isEnabled",
 NULLIF(d."payload"->>'evidenceId','')
FROM claimed d JOIN "notification_endpoints" e ON e."id"=d."endpointId" AND e."storeId"=d."storeId"`, w.options.BatchSize, leaseSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []claimedDelivery{}
	for rows.Next() {
		var item claimedDelivery
		var rawPayload []byte
		if err := rows.Scan(&item.ID, &item.Kind, &item.StoreID, &item.AlertID, &item.RuleID, &item.EndpointID, &item.Provider,
			&item.Priority, &item.FallbackDelaySeconds, &item.TemplateVersion, &rawPayload, &item.AttemptCount, &item.MaxAttempts,
			&item.ProviderAccountRef, &item.DestinationRef, &item.CredentialRef, &item.Config, &item.EndpointEnabled, &item.EvidenceID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(rawPayload, &item.Payload); err != nil {
			item.Payload = RenderPayload{Kind: item.Kind, Provider: item.Provider}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (w *Worker) process(ctx context.Context, item claimedDelivery) error {
	started := w.now().UTC()
	if !item.EndpointEnabled {
		return w.finishFailure(ctx, item, started, &PermanentSendError{Code: WorkerCodeEndpointDisabled})
	}
	sender := w.senders[Provider(item.Provider)]
	if sender == nil {
		return w.finishFailure(ctx, item, started, &PermanentSendError{Code: WorkerCodeSenderUnavailable})
	}
	if item.Kind == DeliveryKindAlert && item.AlertID != nil && item.EvidenceID != nil && w.links != nil {
		link, err := w.links.CreateReviewLink(ctx, ReviewLinkInput{StoreID: item.StoreID, AlertID: *item.AlertID, EvidenceID: *item.EvidenceID, DeliveryID: item.ID})
		if err == nil {
			item.Payload.ReviewURL = link
		} else {
			w.log.Warn("secure review link unavailable; sending notification without link", "deliveryId", item.ID, "error", stableLinkError(err))
		}
	}
	request := SendRequest{
		DeliveryID: item.ID, Provider: Provider(item.Provider), ProviderAccountRef: item.ProviderAccountRef, DestinationRef: item.DestinationRef,
		CredentialRef: item.CredentialRef, Config: item.Config, TemplateVersion: item.TemplateVersion, Payload: item.Payload,
	}
	if request.Provider == ProviderWhatsApp {
		if contract, ok := whatsAppContractForVersion(item.TemplateVersion); ok {
			request.TemplateName = contract.Name
			request.TemplateLanguage = contract.Language
		}
	}
	result, err := sender.Send(ctx, request)
	if err != nil {
		return w.finishFailure(ctx, item, started, err)
	}
	return w.finishSuccess(ctx, item, started, result)
}

func (w *Worker) finishSuccess(ctx context.Context, item claimedDelivery, started time.Time, result SendResult) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	metadata, responseStatus := safeResponseMetadata(result.ResponseMetadata)
	if _, err := tx.Exec(ctx, `INSERT INTO "notification_attempts" ("id","deliveryId","attemptNumber","status","startedAt","finishedAt","durationMs","responseStatus","providerMessageId","responseMetadata") VALUES ($1,$2,$3,'SUCCEEDED',$4,NOW(),$5,$6,$7,$8) ON CONFLICT ("deliveryId","attemptNumber") DO NOTHING`,
		uuid.NewString(), item.ID, item.AttemptCount, started, durationMilliseconds(started, w.now()), responseStatus, nullableString(result.ProviderMessageID), metadata); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE "notification_deliveries" SET "status"='SENT',"providerMessageId"=$1,"sentAt"=NOW(),"lastErrorCode"=NULL,"lastErrorMessage"=NULL,"lockedAt"=NULL,"lockedUntil"=NULL,"updatedAt"=NOW() WHERE "id"=$2 AND "status"='PROCESSING' AND "attemptCount"=$3`, nullableString(result.ProviderMessageID), item.ID, item.AttemptCount)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("notification delivery lease was lost before success was recorded")
	}
	if item.AlertID != nil && item.RuleID != nil {
		if _, err := tx.Exec(ctx, `UPDATE "notification_deliveries" SET "status"='CANCELLED',"lockedAt"=NULL,"lockedUntil"=NULL,"updatedAt"=NOW() WHERE "alertId"=$1 AND "ruleId"=$2 AND "id"<>$3 AND "status"='WAITING_FALLBACK'`, *item.AlertID, *item.RuleID, item.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (w *Worker) finishFailure(ctx context.Context, item claimedDelivery, started time.Time, sendErr error) error {
	code, transient, retryAfter := classifyWorkerError(sendErr)
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO "notification_attempts" ("id","deliveryId","attemptNumber","status","startedAt","finishedAt","durationMs","errorCode","errorMessage","responseMetadata") VALUES ($1,$2,$3,'FAILED',$4,NOW(),$5,$6,$6,'{}'::jsonb) ON CONFLICT ("deliveryId","attemptNumber") DO NOTHING`,
		uuid.NewString(), item.ID, item.AttemptCount, started, durationMilliseconds(started, w.now()), code); err != nil {
		return err
	}
	shouldRetry := transient && item.AttemptCount < item.MaxAttempts
	if shouldRetry {
		delay := w.retryDelay(item.AttemptCount, retryAfter)
		command, err := tx.Exec(ctx, `UPDATE "notification_deliveries" SET "status"='RETRY_SCHEDULED',"availableAt"=NOW()+make_interval(secs=>$1::double precision),"lastErrorCode"=$2,"lastErrorMessage"=$2,"lockedAt"=NULL,"lockedUntil"=NULL,"updatedAt"=NOW() WHERE "id"=$3 AND "status"='PROCESSING' AND "attemptCount"=$4`, delay.Seconds(), code, item.ID, item.AttemptCount)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return errors.New("notification delivery lease was lost before retry was recorded")
		}
		return tx.Commit(ctx)
	}
	command, err := tx.Exec(ctx, `UPDATE "notification_deliveries" SET "status"='FAILED',"lastErrorCode"=$1,"lastErrorMessage"=$1,"lockedAt"=NULL,"lockedUntil"=NULL,"updatedAt"=NOW() WHERE "id"=$2 AND "status"='PROCESSING' AND "attemptCount"=$3`, code, item.ID, item.AttemptCount)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("notification delivery lease was lost before failure was recorded")
	}
	if err := activateFallback(ctx, tx, item); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func activateFallback(ctx context.Context, tx pgx.Tx, item claimedDelivery) error {
	if item.AlertID == nil || item.RuleID == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `WITH next AS (
  SELECT "id" FROM "notification_deliveries"
  WHERE "alertId"=$1 AND "ruleId"=$2 AND "status"='WAITING_FALLBACK' AND "priority">$3
  ORDER BY "priority","createdAt","id" LIMIT 1 FOR UPDATE
)
UPDATE "notification_deliveries" d SET "status"='PENDING',"availableAt"=NOW()+make_interval(secs=>d."fallbackDelaySeconds"::double precision),"updatedAt"=NOW()
FROM next WHERE d."id"=next."id"`, *item.AlertID, *item.RuleID, item.Priority)
	return err
}

func (w *Worker) recoverExpiredLeases(ctx context.Context) error {
	rows, err := w.db.Query(ctx, `SELECT "id","deliveryKind"::text,"alertId","ruleId","priority","attemptCount","maxAttempts" FROM "notification_deliveries" WHERE "status"='PROCESSING' AND "lockedUntil"<=NOW() ORDER BY "lockedUntil" LIMIT $1`, w.options.BatchSize)
	if err != nil {
		return err
	}
	type expired struct {
		id, kind                        string
		alertID, ruleID                 *string
		priority, attempts, maxAttempts int
	}
	items := []expired{}
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.kind, &item.alertID, &item.ruleID, &item.priority, &item.attempts, &item.maxAttempts); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, expired := range items {
		tx, err := w.db.Begin(ctx)
		if err != nil {
			return err
		}
		var lockedAt time.Time
		err = tx.QueryRow(ctx, `SELECT "lockedAt" FROM "notification_deliveries" WHERE "id"=$1 AND "status"='PROCESSING' AND "lockedUntil"<=NOW() FOR UPDATE`, expired.id).Scan(&lockedAt)
		if err == pgx.ErrNoRows {
			_ = tx.Rollback(ctx)
			continue
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO "notification_attempts" ("id","deliveryId","attemptNumber","status","startedAt","finishedAt","durationMs","errorCode","errorMessage","responseMetadata") VALUES ($1,$2,$3,'FAILED',$4::timestamp,NOW(),GREATEST(0, FLOOR(EXTRACT(EPOCH FROM (NOW()::timestamp-$4::timestamp))*1000))::integer,$5,$5,'{}'::jsonb) ON CONFLICT ("deliveryId","attemptNumber") DO NOTHING`, uuid.NewString(), expired.id, expired.attempts, lockedAt, WorkerCodeLeaseExpired)
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if expired.attempts < expired.maxAttempts {
			delay := w.retryDelay(expired.attempts, 0)
			_, err = tx.Exec(ctx, `UPDATE "notification_deliveries" SET "status"='RETRY_SCHEDULED',"availableAt"=NOW()+make_interval(secs=>$1::double precision),"lastErrorCode"=$2,"lastErrorMessage"=$2,"lockedAt"=NULL,"lockedUntil"=NULL,"updatedAt"=NOW() WHERE "id"=$3 AND "status"='PROCESSING'`, delay.Seconds(), WorkerCodeLeaseExpired, expired.id)
		} else {
			_, err = tx.Exec(ctx, `UPDATE "notification_deliveries" SET "status"='FAILED',"lastErrorCode"=$1,"lastErrorMessage"=$1,"lockedAt"=NULL,"lockedUntil"=NULL,"updatedAt"=NOW() WHERE "id"=$2 AND "status"='PROCESSING'`, WorkerCodeLeaseExpired, expired.id)
			if err == nil {
				err = activateFallback(ctx, tx, claimedDelivery{ID: expired.id, Kind: expired.kind, AlertID: expired.alertID, RuleID: expired.ruleID, Priority: expired.priority})
			}
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) retryDelay(attempt int, providerDelay time.Duration) time.Duration {
	exponent := math.Max(0, float64(attempt-1))
	delay := time.Duration(float64(w.options.BaseBackoff) * math.Pow(2, exponent))
	if delay > w.options.MaxBackoff {
		delay = w.options.MaxBackoff
	}
	if providerDelay > delay {
		delay = providerDelay
	}
	if delay > maxProviderRetryDelay {
		delay = maxProviderRetryDelay
	}
	return delay
}

func classifyWorkerError(err error) (string, bool, time.Duration) {
	var transient *TransientSendError
	if errors.As(err, &transient) {
		return stableCode(transient.Code), true, transient.RetryAfter
	}
	var permanent *PermanentSendError
	if errors.As(err, &permanent) {
		return stableCode(permanent.Code), false, 0
	}
	return WorkerCodeUnexpectedError, true, 0
}

func stableCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 {
		return WorkerCodeUnexpectedError
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return WorkerCodeUnexpectedError
		}
	}
	return value
}

func stableLinkError(err error) string {
	if errors.Is(err, ErrSecureLinkUnavailable) {
		return "secure_link_unavailable"
	}
	if errors.Is(err, ErrSecureLinkNotFound) {
		return "secure_link_evidence_not_found"
	}
	return "secure_link_generation_failed"
}

func safeResponseMetadata(input map[string]any) ([]byte, *int) {
	safe := map[string]any{}
	var responseStatus *int
	for _, key := range []string{"httpStatus", "providerStatus"} {
		value, ok := input[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			safe[key] = typed
			if key == "httpStatus" {
				copy := typed
				responseStatus = &copy
			}
		case float64:
			safe[key] = typed
		case string:
			safe[key] = SanitizeText(typed, 100)
		case bool:
			safe[key] = typed
		}
	}
	encoded, _ := json.Marshal(safe)
	return encoded, responseStatus
}

func durationMilliseconds(started, finished time.Time) int {
	duration := finished.Sub(started).Milliseconds()
	if duration < 0 {
		return 0
	}
	if duration > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(duration)
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

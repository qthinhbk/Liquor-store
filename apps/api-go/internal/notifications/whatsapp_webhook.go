package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ProviderReceiptSent      = "SENT"
	ProviderReceiptDelivered = "DELIVERED"
	ProviderReceiptRead      = "READ"
	ProviderReceiptFailed    = "FAILED"
)

type WhatsAppStatusEvent struct {
	ProviderMessageID string
	Status            string
	EventAt           time.Time
	ErrorCode         string
}

type whatsAppWebhookEnvelope struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				Statuses []struct {
					ID        string `json:"id"`
					Status    string `json:"status"`
					Timestamp string `json:"timestamp"`
					Errors    []struct {
						Code int64 `json:"code"`
					} `json:"errors"`
				} `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

func VerifyWhatsAppWebhookSignature(appSecret string, body []byte, signatureHeader string) bool {
	secret := strings.TrimSpace(appSecret)
	signatureHeader = strings.TrimSpace(signatureHeader)
	if secret == "" || !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, "sha256="))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func ParseWhatsAppStatusEvents(body []byte) ([]WhatsAppStatusEvent, error) {
	var envelope whatsAppWebhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("invalid WhatsApp webhook JSON")
	}
	if envelope.Object != "whatsapp_business_account" {
		return nil, errors.New("unexpected WhatsApp webhook object")
	}
	events := make([]WhatsAppStatusEvent, 0)
	for _, entry := range envelope.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			for _, status := range change.Value.Statuses {
				messageID := strings.TrimSpace(status.ID)
				if !safeProviderValue(messageID, 256) {
					continue
				}
				receiptStatus, ok := normalizeWhatsAppReceiptStatus(status.Status)
				if !ok {
					continue
				}
				unixSeconds, err := strconv.ParseInt(strings.TrimSpace(status.Timestamp), 10, 64)
				if err != nil || unixSeconds <= 0 {
					continue
				}
				errorCode := ""
				if len(status.Errors) > 0 && status.Errors[0].Code > 0 {
					errorCode = strconv.FormatInt(status.Errors[0].Code, 10)
				}
				events = append(events, WhatsAppStatusEvent{
					ProviderMessageID: messageID,
					Status:            receiptStatus,
					EventAt:           time.Unix(unixSeconds, 0).UTC(),
					ErrorCode:         errorCode,
				})
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].EventAt.Before(events[j].EventAt) })
	return events, nil
}

func ApplyWhatsAppStatusEvents(ctx context.Context, db *pgxpool.Pool, events []WhatsAppStatusEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	canonicalEvents := make([]WhatsAppStatusEvent, 0, len(events))
	for _, event := range events {
		canonical, err := validateWhatsAppStatusEvent(event)
		if err != nil {
			return 0, err
		}
		canonicalEvents = append(canonicalEvents, canonical)
	}
	sort.SliceStable(canonicalEvents, func(i, j int) bool {
		return canonicalEvents[i].EventAt.Before(canonicalEvents[j].EventAt)
	})
	if db == nil {
		return 0, errors.New("notification database is unavailable")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	applied := 0
	for _, event := range canonicalEvents {
		var deliveryID, deliveryKind, deliveryStatus string
		var alertID, ruleID, currentProviderStatus *string
		var priority int
		err := tx.QueryRow(ctx, `SELECT "id","deliveryKind"::text,"status"::text,"alertId","ruleId","priority","providerStatus"::text
FROM "notification_deliveries"
WHERE "provider"='WHATSAPP' AND "providerMessageId"=$1
FOR UPDATE`, event.ProviderMessageID).Scan(&deliveryID, &deliveryKind, &deliveryStatus, &alertID, &ruleID, &priority, &currentProviderStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		command, err := tx.Exec(ctx, `INSERT INTO "notification_provider_events" ("id","deliveryId","provider","providerMessageId","status","eventAt","errorCode") VALUES ($1,$2,'WHATSAPP',$3,$4,$5,$6) ON CONFLICT ("provider","providerMessageId","status","eventAt") DO NOTHING`,
			uuid.NewString(), deliveryID, event.ProviderMessageID, event.Status, event.EventAt, nullableWebhookValue(event.ErrorCode))
		if err != nil {
			return 0, err
		}
		if command.RowsAffected() == 0 {
			continue
		}
		providerFailureApplies := event.Status == ProviderReceiptFailed &&
			deliveryStatus == StatusSent &&
			(currentProviderStatus == nil || (*currentProviderStatus != ProviderReceiptDelivered && *currentProviderStatus != ProviderReceiptRead))
		_, err = tx.Exec(ctx, `UPDATE "notification_deliveries" SET
"providerStatus"=CASE
  WHEN $2='READ' THEN 'READ'::"NotificationProviderReceiptStatus"
  WHEN $2='DELIVERED' AND COALESCE("providerStatus"::text,'')<>'READ' THEN 'DELIVERED'::"NotificationProviderReceiptStatus"
  WHEN $2='SENT' AND "providerStatus" IS NULL THEN 'SENT'::"NotificationProviderReceiptStatus"
  WHEN $2='FAILED' AND COALESCE("providerStatus"::text,'') NOT IN ('DELIVERED','READ') THEN 'FAILED'::"NotificationProviderReceiptStatus"
  ELSE "providerStatus" END,
"providerStatusAt"=CASE
  WHEN $2='READ'
    OR ($2='DELIVERED' AND COALESCE("providerStatus"::text,'')<>'READ')
    OR ($2='SENT' AND "providerStatus" IS NULL)
    OR ($2='FAILED' AND COALESCE("providerStatus"::text,'') NOT IN ('DELIVERED','READ'))
  THEN $3 ELSE "providerStatusAt" END,
"deliveredAt"=CASE WHEN $2 IN ('DELIVERED','READ') THEN LEAST(COALESCE("deliveredAt",$3),$3) ELSE "deliveredAt" END,
"readAt"=CASE WHEN $2='READ' THEN LEAST(COALESCE("readAt",$3),$3) ELSE "readAt" END,
"providerFailedAt"=CASE WHEN $2='FAILED' AND COALESCE("providerStatus"::text,'') NOT IN ('DELIVERED','READ') THEN LEAST(COALESCE("providerFailedAt",$3),$3) ELSE "providerFailedAt" END,
"providerErrorCode"=CASE
  WHEN $2='FAILED' AND COALESCE("providerStatus"::text,'') NOT IN ('DELIVERED','READ') THEN $4
  WHEN $2 IN ('DELIVERED','READ') THEN NULL
  ELSE "providerErrorCode" END,
"updatedAt"=NOW()
WHERE "id"=$1`, deliveryID, event.Status, event.EventAt, nullableWebhookValue(event.ErrorCode))
		if err != nil {
			return 0, err
		}
		if providerFailureApplies {
			command, err := tx.Exec(ctx, `UPDATE "notification_deliveries" SET
"status"='FAILED',
"lastErrorCode"='whatsapp_delivery_failed',
"lastErrorMessage"='whatsapp_delivery_failed',
"lockedAt"=NULL,
"lockedUntil"=NULL,
"updatedAt"=NOW()
WHERE "id"=$1 AND "status"='SENT'`, deliveryID)
			if err != nil {
				return 0, err
			}
			if command.RowsAffected() == 1 && deliveryKind == DeliveryKindAlert {
				if err := activateFallbackAfterProviderFailure(ctx, tx, alertID, ruleID, priority); err != nil {
					return 0, err
				}
			}
		}
		applied++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return applied, nil
}

func validateWhatsAppStatusEvent(event WhatsAppStatusEvent) (WhatsAppStatusEvent, error) {
	event.ProviderMessageID = strings.TrimSpace(event.ProviderMessageID)
	if !safeProviderValue(event.ProviderMessageID, 256) {
		return WhatsAppStatusEvent{}, errors.New("invalid WhatsApp provider message ID")
	}
	status, ok := normalizeWhatsAppReceiptStatus(event.Status)
	if !ok {
		return WhatsAppStatusEvent{}, errors.New("invalid WhatsApp receipt status")
	}
	event.Status = status
	if event.EventAt.IsZero() || event.EventAt.Unix() <= 0 {
		return WhatsAppStatusEvent{}, errors.New("invalid WhatsApp receipt timestamp")
	}
	event.EventAt = event.EventAt.UTC()
	event.ErrorCode = strings.TrimSpace(event.ErrorCode)
	if event.ErrorCode != "" {
		if len(event.ErrorCode) > 120 {
			return WhatsAppStatusEvent{}, errors.New("invalid WhatsApp error code")
		}
		for _, current := range event.ErrorCode {
			if current < '0' || current > '9' {
				return WhatsAppStatusEvent{}, errors.New("invalid WhatsApp error code")
			}
		}
	}
	return event, nil
}

func activateFallbackAfterProviderFailure(ctx context.Context, tx pgx.Tx, alertID, ruleID *string, priority int) error {
	if alertID == nil || ruleID == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `WITH next AS (
  SELECT "id" FROM "notification_deliveries"
  WHERE "alertId"=$1 AND "ruleId"=$2
    AND "status" IN ('WAITING_FALLBACK','CANCELLED')
    AND "priority">$3
  ORDER BY "priority","createdAt","id" LIMIT 1 FOR UPDATE
)
UPDATE "notification_deliveries" d SET
  "status"='PENDING',
  "availableAt"=NOW()+make_interval(secs=>d."fallbackDelaySeconds"::double precision),
  "lastErrorCode"=NULL,
  "lastErrorMessage"=NULL,
  "lockedAt"=NULL,
  "lockedUntil"=NULL,
  "updatedAt"=NOW()
FROM next WHERE d."id"=next."id"`, *alertID, *ruleID, priority)
	return err
}

func normalizeWhatsAppReceiptStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sent":
		return ProviderReceiptSent, true
	case "delivered":
		return ProviderReceiptDelivered, true
	case "read":
		return ProviderReceiptRead, true
	case "failed":
		return ProviderReceiptFailed, true
	default:
		return "", false
	}
}

func safeProviderValue(value string, maxRunes int) bool {
	if value == "" || len([]rune(value)) > maxRunes {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) || unicode.IsSpace(current) {
			return false
		}
	}
	return true
}

func nullableWebhookValue(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

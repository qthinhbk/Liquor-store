package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/liquor-store/security-api/internal/notifications"
)

const (
	maxAIEventIDRunes     = 200
	maxAICorrelationRunes = 200
	maxAIMetadataBytes    = 64 << 10
	maxAIEvidenceItems    = 5
	maxStorageKeyRunes    = 500
	maxEvidenceSeconds    = 10 * 60
)

type aiAlertInput struct {
	SourceEventID         string            `json:"sourceEventId"`
	CorrelationID         *string           `json:"correlationId"`
	StoreID               string            `json:"storeId"`
	CameraID              string            `json:"cameraId"`
	ZoneID                *string           `json:"zoneId"`
	Type                  string            `json:"type"`
	Severity              string            `json:"severity"`
	SubjectPersonCategory string            `json:"subjectPersonCategory"`
	Confidence            *float64          `json:"confidence"`
	DetectedAt            time.Time         `json:"detectedAt"`
	ObservedStartAt       *time.Time        `json:"observedStartAt"`
	ObservedEndAt         *time.Time        `json:"observedEndAt"`
	Metadata              json.RawMessage   `json:"metadata"`
	Evidence              []aiEvidenceInput `json:"evidence"`
}

type aiEvidenceInput struct {
	StorageKey      string    `json:"storageKey"`
	MimeType        string    `json:"mimeType"`
	DurationSeconds int       `json:"durationSeconds"`
	StartsAt        time.Time `json:"startsAt"`
	EndsAt          time.Time `json:"endsAt"`
}

type aiDeliverySummary struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Priority int    `json:"priority"`
	Status   string `json:"status"`
}

type aiAlertIngestResponse struct {
	AlertID       string              `json:"alertId"`
	SourceEventID string              `json:"sourceEventId"`
	Created       bool                `json:"created"`
	Deliveries    []aiDeliverySummary `json:"deliveries"`
}

func (s *Server) aiIngestEndpoint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		expected := strings.TrimSpace(s.config.AIIngestToken)
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if expected == "" || !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "Unauthorized", "AI service authentication is required.")
			return
		}
		actual := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if len(actual) != len(expected) || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, "Unauthorized", "AI service authentication is invalid.")
			return
		}
		allowed, retryAfter := s.limits.allow("ai-ingest:"+s.clientIP(r), 120, time.Minute, time.Now())
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second).Seconds()))))
			writeError(w, http.StatusTooManyRequests, "Too Many Requests", "AI alert ingestion rate limit exceeded.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ingestAIAlert(w http.ResponseWriter, r *http.Request) {
	var input aiAlertInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if message := normalizeAndValidateAIAlert(&input); message != "" {
		writeError(w, http.StatusBadRequest, "Bad Request", message)
		return
	}

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var storeName, storeTimezone, cameraName string
	err = tx.QueryRow(ctx, `SELECT s."name",s."timezone",c."name" FROM "stores" s JOIN "cameras" c ON c."storeId"=s."id" WHERE s."id"=$1 AND c."id"=$2 AND c."isEnabled"`, input.StoreID, input.CameraID).Scan(&storeName, &storeTimezone, &cameraName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Not Found", "Store or enabled camera was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	if input.ZoneID != nil {
		var validZone bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "camera_zones" WHERE "id"=$1 AND "cameraId"=$2 AND "isEnabled")`, *input.ZoneID, input.CameraID).Scan(&validZone); err != nil {
			s.internalError(w, err)
			return
		}
		if !validZone {
			writeError(w, http.StatusNotFound, "Not Found", "Enabled camera zone was not found.")
			return
		}
	}

	alertID := uuid.NewString()
	metadata := input.Metadata
	if len(metadata) == 0 || string(metadata) == "null" {
		metadata = json.RawMessage("{}")
	}
	err = tx.QueryRow(ctx, `INSERT INTO "alerts" ("id","sourceEventId","correlationId","storeId","cameraId","zoneId","type","severity","subjectPersonCategory","confidence","detectedAt","observedStartAt","observedEndAt","metadata","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NOW()) ON CONFLICT ("sourceEventId") DO NOTHING RETURNING "id"`,
		alertID, input.SourceEventID, nullableString(input.CorrelationID), input.StoreID, input.CameraID, nullableString(input.ZoneID), input.Type, input.Severity, input.SubjectPersonCategory, input.Confidence, input.DetectedAt.UTC(), nullableTime(input.ObservedStartAt), nullableTime(input.ObservedEndAt), metadata).Scan(&alertID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingStoreID string
		if err := tx.QueryRow(ctx, `SELECT "id","storeId" FROM "alerts" WHERE "sourceEventId"=$1`, input.SourceEventID).Scan(&alertID, &existingStoreID); err != nil {
			s.internalError(w, err)
			return
		}
		if existingStoreID != input.StoreID {
			writeError(w, http.StatusConflict, "Conflict", "sourceEventId is already assigned to another store.")
			return
		}
		deliveries, loadErr := loadAIDeliveries(ctx, tx, alertID)
		if loadErr != nil {
			s.internalError(w, loadErr)
			return
		}
		writeJSON(w, http.StatusOK, aiAlertIngestResponse{AlertID: alertID, SourceEventID: input.SourceEventID, Created: false, Deliveries: deliveries})
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}

	primaryEvidenceID := ""
	primaryStorageKey := ""
	for _, evidence := range input.Evidence {
		evidenceID := uuid.NewString()
		_, err = tx.Exec(ctx, `INSERT INTO "alert_evidence" ("id","alertId","storageKey","mimeType","durationSeconds","startsAt","endsAt") VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			evidenceID, alertID, evidence.StorageKey, evidence.MimeType, evidence.DurationSeconds, evidence.StartsAt.UTC(), evidence.EndsAt.UTC())
		if err != nil {
			if uniqueViolation(err) {
				writeError(w, http.StatusConflict, "Conflict", "Evidence storageKey already exists.")
				return
			}
			s.internalError(w, err)
			return
		}
		if primaryEvidenceID == "" {
			primaryEvidenceID = evidenceID
			primaryStorageKey = evidence.StorageKey
		}
	}

	summaries, err := notifications.EnqueueAlertTx(ctx, tx, notifications.AlertNotificationInput{
		StoreID: input.StoreID, StoreName: storeName, StoreTimezone: storeTimezone,
		AlertID: alertID, CorrelationID: stringValue(input.CorrelationID), AlertType: input.Type,
		Severity: input.Severity, DetectedAt: input.DetectedAt.UTC(), SubjectPersonCategory: input.SubjectPersonCategory,
		CameraID: input.CameraID, CameraName: cameraName, EvidenceID: primaryEvidenceID, StorageKey: primaryStorageKey,
	})
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, err)
		return
	}
	deliveries := make([]aiDeliverySummary, 0, len(summaries))
	for _, summary := range summaries {
		deliveries = append(deliveries, aiDeliverySummary{ID: summary.ID, Provider: summary.Provider, Priority: summary.Priority, Status: summary.Status})
	}
	writeJSON(w, http.StatusCreated, aiAlertIngestResponse{AlertID: alertID, SourceEventID: input.SourceEventID, Created: true, Deliveries: deliveries})
}

type aiDeliveryQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadAIDeliveries(ctx context.Context, queryer aiDeliveryQueryer, alertID string) ([]aiDeliverySummary, error) {
	rows, err := queryer.Query(ctx, `SELECT "id","provider"::text,"priority","status"::text FROM "notification_deliveries" WHERE "alertId"=$1 ORDER BY "priority","id"`, alertID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []aiDeliverySummary{}
	for rows.Next() {
		var item aiDeliverySummary
		if err := rows.Scan(&item.ID, &item.Provider, &item.Priority, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeAndValidateAIAlert(input *aiAlertInput) string {
	input.SourceEventID = strings.TrimSpace(input.SourceEventID)
	input.StoreID = strings.TrimSpace(input.StoreID)
	input.CameraID = strings.TrimSpace(input.CameraID)
	input.Type = strings.ToUpper(strings.TrimSpace(input.Type))
	input.Severity = strings.ToUpper(strings.TrimSpace(input.Severity))
	input.SubjectPersonCategory = strings.ToUpper(strings.TrimSpace(input.SubjectPersonCategory))
	if input.SubjectPersonCategory == "" {
		input.SubjectPersonCategory = "UNKNOWN"
	}
	input.CorrelationID = normalizeOptionalString(input.CorrelationID)
	input.ZoneID = normalizeOptionalString(input.ZoneID)

	if !validLength(input.SourceEventID, 1, maxAIEventIDRunes) {
		return "sourceEventId must contain 1 to 200 characters."
	}
	if input.CorrelationID != nil && !validLength(*input.CorrelationID, 1, maxAICorrelationRunes) {
		return "correlationId must contain at most 200 characters."
	}
	if _, err := uuid.Parse(input.StoreID); err != nil {
		return "storeId must be a UUID."
	}
	if _, err := uuid.Parse(input.CameraID); err != nil {
		return "cameraId must be a UUID."
	}
	if input.ZoneID != nil {
		if _, err := uuid.Parse(*input.ZoneID); err != nil {
			return "zoneId must be a UUID."
		}
	}
	if !oneOf(input.Type, alertTypeValues...) {
		return "type is not a supported alert type."
	}
	if !oneOf(input.Severity, "LOW", "MEDIUM", "HIGH", "CRITICAL") {
		return "severity is not supported."
	}
	if !oneOf(input.SubjectPersonCategory, "EMPLOYEE", "CUSTOMER", "UNKNOWN") {
		return "subjectPersonCategory is not supported."
	}
	if input.Confidence != nil && (math.IsNaN(*input.Confidence) || math.IsInf(*input.Confidence, 0) || *input.Confidence < 0 || *input.Confidence > 1) {
		return "confidence must be between 0 and 1."
	}
	if input.DetectedAt.IsZero() {
		return "detectedAt is required."
	}
	if input.ObservedStartAt != nil && input.ObservedEndAt != nil && input.ObservedStartAt.After(*input.ObservedEndAt) {
		return "observedStartAt must not be after observedEndAt."
	}
	if len(input.Metadata) > maxAIMetadataBytes {
		return "metadata must be at most 65536 bytes."
	}
	if len(input.Metadata) > 0 && string(input.Metadata) != "null" {
		var object map[string]any
		if err := json.Unmarshal(input.Metadata, &object); err != nil || object == nil {
			return "metadata must be a JSON object."
		}
	}
	if len(input.Evidence) < 1 || len(input.Evidence) > maxAIEvidenceItems {
		return "evidence must contain between 1 and 5 video clips."
	}
	seenKeys := make(map[string]struct{}, len(input.Evidence))
	for index := range input.Evidence {
		evidence := &input.Evidence[index]
		evidence.StorageKey = strings.TrimSpace(evidence.StorageKey)
		evidence.MimeType = strings.ToLower(strings.TrimSpace(evidence.MimeType))
		if !validEvidenceStorageKey(evidence.StorageKey) {
			return "evidence storageKey is invalid."
		}
		if _, duplicate := seenKeys[evidence.StorageKey]; duplicate {
			return "evidence storageKey values must be unique."
		}
		seenKeys[evidence.StorageKey] = struct{}{}
		if !oneOf(evidence.MimeType, "video/mp4", "video/webm") {
			return "evidence mimeType must be video/mp4 or video/webm."
		}
		if evidence.DurationSeconds < 1 || evidence.DurationSeconds > maxEvidenceSeconds {
			return "evidence durationSeconds must be between 1 and 600."
		}
		if evidence.StartsAt.IsZero() || evidence.EndsAt.IsZero() || !evidence.StartsAt.Before(evidence.EndsAt) {
			return "evidence startsAt must be before endsAt."
		}
	}
	return ""
}

func validEvidenceStorageKey(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > maxStorageKeyRunes || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "://") || strings.ContainsRune(value, 0) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liquor-store/security-api/internal/notifications"
)

type notificationDeliveryResponse struct {
	ID                string     `json:"id"`
	DeliveryKind      string     `json:"deliveryKind"`
	AlertID           *string    `json:"alertId"`
	RuleID            *string    `json:"ruleId"`
	EndpointID        string     `json:"endpointId"`
	EndpointLabel     string     `json:"endpointLabel"`
	DestinationMasked string     `json:"destinationMasked"`
	Provider          string     `json:"provider"`
	Priority          int        `json:"priority"`
	Status            string     `json:"status"`
	TemplateVersion   string     `json:"templateVersion"`
	AttemptCount      int        `json:"attemptCount"`
	MaxAttempts       int        `json:"maxAttempts"`
	AvailableAt       time.Time  `json:"availableAt"`
	LastAttemptAt     *time.Time `json:"lastAttemptAt"`
	SentAt            *time.Time `json:"sentAt"`
	LastErrorCode     *string    `json:"lastErrorCode"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

type notificationAttemptResponse struct {
	ID                string          `json:"id"`
	AttemptNumber     int             `json:"attemptNumber"`
	Status            string          `json:"status"`
	StartedAt         time.Time       `json:"startedAt"`
	FinishedAt        time.Time       `json:"finishedAt"`
	DurationMs        int             `json:"durationMs"`
	ResponseStatus    *int            `json:"responseStatus"`
	ProviderMessageID *string         `json:"providerMessageId"`
	ErrorCode         *string         `json:"errorCode"`
	ResponseMetadata  json.RawMessage `json:"responseMetadata"`
}

func (s *Server) reviewNotificationEvidence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.review == nil {
		writeError(w, http.StatusServiceUnavailable, "Service Unavailable", "Review video is temporarily unavailable.")
		return
	}
	s.review.ServeToken(w, r, r.PathValue("token"))
}

func (s *Server) createEvidencePlaybackURL(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, alertID, evidenceID := r.PathValue("storeId"), r.PathValue("alertId"), r.PathValue("evidenceId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OPERATOR") {
		return
	}
	if s.review == nil {
		writeError(w, http.StatusServiceUnavailable, "Service Unavailable", "Secure evidence playback is not configured.")
		return
	}
	link, err := s.review.CreateReviewLink(r.Context(), notifications.ReviewLinkInput{StoreID: storeID, AlertID: alertID, EvidenceID: evidenceID})
	if err != nil {
		if err == notifications.ErrSecureLinkNotFound || err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Alert evidence was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"url": link, "expiresInSeconds": int(s.config.SecureVideoLinkTTL.Seconds())})
}

func (s *Server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	conditions := []string{`d."storeId"=$1`}
	values := []any{storeID}
	add := func(clause string, value any) {
		values = append(values, value)
		conditions = append(conditions, clause+`$`+strconv.Itoa(len(values)))
	}
	query := r.URL.Query()
	if value := query.Get("status"); value != "" {
		if !oneOf(value, notifications.StatusWaitingFallback, notifications.StatusPending, notifications.StatusProcessing, notifications.StatusRetryScheduled, notifications.StatusSent, notifications.StatusFailed, notifications.StatusCancelled) {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid delivery status filter.")
			return
		}
		add(`d."status"=`, value)
	}
	if value := query.Get("provider"); value != "" {
		if !oneOf(value, string(notifications.ProviderTelegram), string(notifications.ProviderWhatsApp)) {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid notification provider filter.")
			return
		}
		add(`d."provider"=`, value)
	}
	if value := query.Get("kind"); value != "" {
		if !oneOf(value, notifications.DeliveryKindAlert, notifications.DeliveryKindTest) {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid delivery kind filter.")
			return
		}
		add(`d."deliveryKind"=`, value)
	}
	if value := strings.TrimSpace(query.Get("alertId")); value != "" {
		add(`d."alertId"=`, value)
	}
	if value := query.Get("cursor"); value != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		parts := strings.SplitN(string(decoded), "|", 2)
		parsed, parseErr := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil || len(parts) != 2 || parseErr != nil || strings.TrimSpace(parts[1]) == "" {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid delivery cursor.")
			return
		}
		values = append(values, parsed, parts[1])
		conditions = append(conditions, `(d."createdAt",d."id")<($`+strconv.Itoa(len(values)-1)+`,$`+strconv.Itoa(len(values))+`)`)
	}
	limit := 30
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "Bad Request", "limit must be between 1 and 100.")
			return
		}
		limit = parsed
	}
	values = append(values, limit+1)
	sql := `SELECT d."id",d."deliveryKind"::text,d."alertId",d."ruleId",d."endpointId",e."label",e."destinationRef",d."provider"::text,d."priority",d."status"::text,d."templateVersion",d."attemptCount",d."maxAttempts",d."availableAt",d."lastAttemptAt",d."sentAt",d."lastErrorCode",d."createdAt",d."updatedAt" FROM "notification_deliveries" d JOIN "notification_endpoints" e ON e."id"=d."endpointId" AND e."storeId"=d."storeId" WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY d."createdAt" DESC,d."id" DESC LIMIT $` + strconv.Itoa(len(values))
	rows, err := s.db.Query(r.Context(), sql, values...)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []notificationDeliveryResponse{}
	for rows.Next() {
		var item notificationDeliveryResponse
		var destination string
		if err := rows.Scan(&item.ID, &item.DeliveryKind, &item.AlertID, &item.RuleID, &item.EndpointID, &item.EndpointLabel, &destination, &item.Provider, &item.Priority, &item.Status, &item.TemplateVersion, &item.AttemptCount, &item.MaxAttempts, &item.AvailableAt, &item.LastAttemptAt, &item.SentAt, &item.LastErrorCode, &item.CreatedAt, &item.UpdatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		item.DestinationMasked = notifications.MaskDestination(destination)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	var nextCursor *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		cursor := base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.Format(time.RFC3339Nano) + "|" + last.ID))
		nextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nextCursor})
}

func (s *Server) findNotificationDelivery(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, deliveryID := r.PathValue("storeId"), r.PathValue("deliveryId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var item notificationDeliveryResponse
	var destination string
	err := s.db.QueryRow(r.Context(), `SELECT d."id",d."deliveryKind"::text,d."alertId",d."ruleId",d."endpointId",e."label",e."destinationRef",d."provider"::text,d."priority",d."status"::text,d."templateVersion",d."attemptCount",d."maxAttempts",d."availableAt",d."lastAttemptAt",d."sentAt",d."lastErrorCode",d."createdAt",d."updatedAt" FROM "notification_deliveries" d JOIN "notification_endpoints" e ON e."id"=d."endpointId" AND e."storeId"=d."storeId" WHERE d."id"=$1 AND d."storeId"=$2`, deliveryID, storeID).Scan(&item.ID, &item.DeliveryKind, &item.AlertID, &item.RuleID, &item.EndpointID, &item.EndpointLabel, &destination, &item.Provider, &item.Priority, &item.Status, &item.TemplateVersion, &item.AttemptCount, &item.MaxAttempts, &item.AvailableAt, &item.LastAttemptAt, &item.SentAt, &item.LastErrorCode, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Notification delivery was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	item.DestinationMasked = notifications.MaskDestination(destination)
	rows, err := s.db.Query(r.Context(), `SELECT "id","attemptNumber","status"::text,"startedAt","finishedAt","durationMs","responseStatus","providerMessageId","errorCode","responseMetadata" FROM "notification_attempts" WHERE "deliveryId"=$1 ORDER BY "attemptNumber"`, deliveryID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	attempts := []notificationAttemptResponse{}
	for rows.Next() {
		var attempt notificationAttemptResponse
		if err := rows.Scan(&attempt.ID, &attempt.AttemptNumber, &attempt.Status, &attempt.StartedAt, &attempt.FinishedAt, &attempt.DurationMs, &attempt.ResponseStatus, &attempt.ProviderMessageID, &attempt.ErrorCode, &attempt.ResponseMetadata); err != nil {
			s.internalError(w, err)
			return
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": item, "attempts": attempts})
}

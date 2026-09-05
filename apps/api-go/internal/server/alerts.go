package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var alertTypeValues = []string{"CASH_DRAWER_WITHOUT_CUSTOMER", "SUSPICIOUS_CASH_HANDLING", "POS_VOID_OR_REFUND", "UNAUTHORIZED_STOCKROOM_ACCESS", "HIGH_VALUE_ZONE_DWELL", "WEAPON_DETECTED", "SUSPICIOUS_PRODUCT_CONCEALMENT"}

type alertDetailResponse struct {
	alertResponse
	StoreID            string             `json:"storeId"`
	ObservedStartAt    *time.Time         `json:"observedStartAt"`
	ObservedEndAt      *time.Time         `json:"observedEndAt"`
	AcknowledgedByID   *string            `json:"acknowledgedById"`
	AcknowledgedByName *string            `json:"acknowledgedByName"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
	Evidence           []evidenceResponse `json:"evidence"`
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OPERATOR") {
		return
	}
	query, values, limit, ok := buildAlertQuery(w, r.URL.Query(), storeID)
	if !ok {
		return
	}
	rows, err := s.db.Query(r.Context(), query, values...)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []alertResponse{}
	for rows.Next() {
		var item alertResponse
		if err := scanAlert(rows, &item); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	var nextCursor *string
	if len(items) > limit {
		items = items[:limit]
		value := items[len(items)-1].DetectedAt.Format(time.RFC3339Nano)
		nextCursor = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nextCursor})
}

func buildAlertQuery(w http.ResponseWriter, q url.Values, storeID string) (string, []any, int, bool) {
	conditions := []string{`a."storeId"=$1`}
	values := []any{storeID}
	add := func(column string, value any) {
		values = append(values, value)
		conditions = append(conditions, column+`=$`+itoa(len(values)))
	}
	if value := q.Get("status"); value != "" {
		if !oneOf(value, "NEW", "ACKNOWLEDGED", "DISMISSED", "RESOLVED") {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid alert status filter.")
			return "", nil, 0, false
		}
		add(`a."status"`, value)
	}
	if value := q.Get("severity"); value != "" {
		if !oneOf(value, "LOW", "MEDIUM", "HIGH", "CRITICAL") {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid alert severity filter.")
			return "", nil, 0, false
		}
		add(`a."severity"`, value)
	}
	if value := q.Get("type"); value != "" {
		if !oneOf(value, alertTypeValues...) {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid alert type filter.")
			return "", nil, 0, false
		}
		add(`a."type"`, value)
	}
	if value := q.Get("subjectPersonCategory"); value != "" {
		if !oneOf(value, "EMPLOYEE", "CUSTOMER", "UNKNOWN") {
			writeError(w, http.StatusBadRequest, "Bad Request", "Invalid person category filter.")
			return "", nil, 0, false
		}
		add(`a."subjectPersonCategory"`, value)
	}
	if value := q.Get("cameraId"); value != "" {
		add(`a."cameraId"`, value)
	}
	for _, name := range []string{"from", "to", "cursor"} {
		if value := q.Get(name); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeError(w, http.StatusBadRequest, "Bad Request", name+" must be an ISO-8601 timestamp.")
				return "", nil, 0, false
			}
			values = append(values, parsed)
			operator := ">="
			if name == "to" {
				operator = "<="
			}
			if name == "cursor" {
				operator = "<"
			}
			conditions = append(conditions, `a."detectedAt" `+operator+` $`+itoa(len(values)))
		}
	}
	limit := 30
	if value := q.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "Bad Request", "limit must be between 1 and 100.")
			return "", nil, 0, false
		}
		limit = parsed
	}
	values = append(values, limit+1)
	query := `SELECT a."id",a."sourceEventId",a."correlationId",a."type",a."severity",a."status",a."subjectPersonCategory",a."confidence"::float8,a."detectedAt",a."acknowledgedAt",a."resolutionNote",COALESCE(a."metadata",'null'::jsonb),c."id",c."name",z."id",z."name",EXISTS(SELECT 1 FROM "alert_evidence" e WHERE e."alertId"=a."id" AND e."mimeType" LIKE 'video/%') FROM "alerts" a LEFT JOIN "cameras" c ON c."id"=a."cameraId" LEFT JOIN "camera_zones" z ON z."id"=a."zoneId" WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY a."detectedAt" DESC LIMIT $` + itoa(len(values))
	return query, values, limit, true
}

type rowScanner interface{ Scan(dest ...any) error }

func scanAlert(row rowScanner, item *alertResponse) error {
	return row.Scan(&item.ID, &item.SourceEventID, &item.CorrelationID, &item.Type, &item.Severity, &item.Status, &item.SubjectPersonCategory, &item.Confidence, &item.DetectedAt, &item.AcknowledgedAt, &item.ResolutionNote, &item.Metadata, &item.CameraID, &item.CameraName, &item.ZoneID, &item.ZoneName, &item.HasVideoEvidence)
}

func (s *Server) findAlert(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, alertID := r.PathValue("storeId"), r.PathValue("alertId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OPERATOR") {
		return
	}
	detail, err := s.loadAlertDetail(r.Context(), storeID, alertID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Alert was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) loadAlertDetail(ctx context.Context, storeID, alertID string) (alertDetailResponse, error) {
	var item alertDetailResponse
	err := s.db.QueryRow(ctx, `SELECT a."id",a."sourceEventId",a."correlationId",a."type",a."severity",a."status",a."subjectPersonCategory",a."confidence"::float8,a."detectedAt",a."acknowledgedAt",a."resolutionNote",COALESCE(a."metadata",'null'::jsonb),c."id",c."name",z."id",z."name",EXISTS(SELECT 1 FROM "alert_evidence" e WHERE e."alertId"=a."id" AND e."mimeType" LIKE 'video/%'),a."storeId",a."observedStartAt",a."observedEndAt",a."acknowledgedById",u."displayName",a."createdAt",a."updatedAt" FROM "alerts" a LEFT JOIN "cameras" c ON c."id"=a."cameraId" LEFT JOIN "camera_zones" z ON z."id"=a."zoneId" LEFT JOIN "users" u ON u."id"=a."acknowledgedById" WHERE a."id"=$1 AND a."storeId"=$2`, alertID, storeID).Scan(&item.ID, &item.SourceEventID, &item.CorrelationID, &item.Type, &item.Severity, &item.Status, &item.SubjectPersonCategory, &item.Confidence, &item.DetectedAt, &item.AcknowledgedAt, &item.ResolutionNote, &item.Metadata, &item.CameraID, &item.CameraName, &item.ZoneID, &item.ZoneName, &item.HasVideoEvidence, &item.StoreID, &item.ObservedStartAt, &item.ObservedEndAt, &item.AcknowledgedByID, &item.AcknowledgedByName, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return alertDetailResponse{}, err
	}
	rows, err := s.db.Query(ctx, `SELECT "id","storageKey","mimeType","durationSeconds","startsAt","endsAt" FROM "alert_evidence" WHERE "alertId"=$1 ORDER BY "startsAt"`, alertID)
	if err != nil {
		return alertDetailResponse{}, err
	}
	defer rows.Close()
	item.Evidence = []evidenceResponse{}
	for rows.Next() {
		var evidence evidenceResponse
		if err := rows.Scan(&evidence.ID, &evidence.StorageKey, &evidence.MimeType, &evidence.DurationSeconds, &evidence.StartsAt, &evidence.EndsAt); err != nil {
			return alertDetailResponse{}, err
		}
		item.Evidence = append(item.Evidence, evidence)
	}
	return item, rows.Err()
}

func (s *Server) acknowledgeAlert(w http.ResponseWriter, r *http.Request, user principal) {
	s.changeAlertStatus(w, r, user, "ACKNOWLEDGED", "OPERATOR")
}
func (s *Server) dismissAlert(w http.ResponseWriter, r *http.Request, user principal) {
	s.changeAlertStatus(w, r, user, "DISMISSED", "OPERATOR")
}
func (s *Server) resolveAlert(w http.ResponseWriter, r *http.Request, user principal) {
	s.changeAlertStatus(w, r, user, "RESOLVED", "MANAGER")
}

func (s *Server) changeAlertStatus(w http.ResponseWriter, r *http.Request, user principal, status, minimumRole string) {
	storeID, alertID := r.PathValue("storeId"), r.PathValue("alertId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, minimumRole) {
		return
	}
	var input struct {
		Note *string `json:"note"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var note any
	if input.Note != nil {
		trimmed := strings.TrimSpace(*input.Note)
		if len(trimmed) > 1000 {
			writeError(w, http.StatusBadRequest, "Bad Request", "note must be at most 1000 characters.")
			return
		}
		if trimmed != "" {
			note = trimmed
		}
	}
	var response struct {
		ID             string    `json:"id"`
		Status         string    `json:"status"`
		AcknowledgedAt time.Time `json:"acknowledgedAt"`
		ResolutionNote *string   `json:"resolutionNote"`
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var role string
	err = tx.QueryRow(r.Context(), `SELECT sm."role" FROM "store_memberships" sm JOIN "users" u ON u."id"=sm."userId" WHERE sm."userId"=$1 AND sm."storeId"=$2 AND u."status"='ACTIVE' FOR SHARE OF sm,u`, user.ID, storeID).Scan(&role)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusForbidden, "Forbidden", "Your store role does not allow this action.")
		} else {
			s.internalError(w, err)
		}
		return
	}
	var previousStatus string
	var previousActor, previousNote *string
	var previousAt *time.Time
	err = tx.QueryRow(r.Context(), `SELECT "status","acknowledgedById","acknowledgedAt","resolutionNote" FROM "alerts" WHERE "id"=$1 AND "storeId"=$2 FOR UPDATE`, alertID, storeID).Scan(&previousStatus, &previousActor, &previousAt, &previousNote)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Alert was not found.")
		} else {
			s.internalError(w, err)
		}
		return
	}
	if code := alertTransitionStatus(previousStatus, status, role); code != 0 {
		writeError(w, code, http.StatusText(code), "This alert decision cannot be changed with your current role or from its current state.")
		return
	}
	if input.Note == nil {
		note = previousNote
	}
	err = tx.QueryRow(r.Context(), `UPDATE "alerts" SET "status"=$1,"acknowledgedAt"=NOW(),"acknowledgedById"=$2,"resolutionNote"=$3,"updatedAt"=NOW() WHERE "id"=$4 AND "storeId"=$5 RETURNING "id","status","acknowledgedAt","resolutionNote"`, status, user.ID, note, alertID, storeID).Scan(&response.ID, &response.Status, &response.AcknowledgedAt, &response.ResolutionNote)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Alert was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(), `INSERT INTO "alert_status_history" ("id","storeId","alertId","actorId","actorRole","previousStatus","newStatus","previousActorId","previousAcknowledgedAt","previousNote","note") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, uuid.NewString(), storeID, alertID, user.ID, role, previousStatus, status, previousActor, previousAt, previousNote, note); err != nil {
		s.internalError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, response)
}

// Terminal decisions can only be corrected by management. Repeating a decision
// never rewrites its actor/note; row locking serializes competing transitions.
func alertTransitionStatus(from, to, role string) int {
	if roleRank[role] < roleRank["OPERATOR"] || (to == "RESOLVED" && roleRank[role] < roleRank["MANAGER"]) ||
		(oneOf(from, "RESOLVED", "DISMISSED") && roleRank[role] < roleRank["MANAGER"]) {
		return http.StatusForbidden
	}
	if from == to || !oneOf(from, "NEW", "ACKNOWLEDGED", "RESOLVED", "DISMISSED") || !oneOf(to, "ACKNOWLEDGED", "RESOLVED", "DISMISSED") ||
		(oneOf(from, "RESOLVED", "DISMISSED") && to == "ACKNOWLEDGED") {
		return http.StatusConflict
	}
	return 0
}

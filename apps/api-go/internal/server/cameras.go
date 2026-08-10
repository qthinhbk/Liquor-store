package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type cameraCreateRequest struct {
	Name             string `json:"name"`
	Location         string `json:"location"`
	Protocol         string `json:"protocol"`
	StreamGatewayRef string `json:"streamGatewayRef"`
	IsEnabled        *bool  `json:"isEnabled"`
}

type cameraUpdateRequest struct {
	Name             *string `json:"name"`
	Location         *string `json:"location"`
	Protocol         *string `json:"protocol"`
	StreamGatewayRef *string `json:"streamGatewayRef"`
	Status           *string `json:"status"`
	IsEnabled        *bool   `json:"isEnabled"`
}

type zoneRequest struct {
	Name                   *string         `json:"name"`
	Kind                   *string         `json:"kind"`
	ExpectedPersonCategory *string         `json:"expectedPersonCategory"`
	Polygon                json.RawMessage `json:"polygon"`
	DwellThresholdSeconds  *int            `json:"dwellThresholdSeconds"`
}

func (s *Server) listCameras(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OPERATOR") {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT "id","name","location","protocol","streamGatewayRef","status","isEnabled","createdAt","updatedAt" FROM "cameras" WHERE "storeId"=$1 ORDER BY "name"`, storeID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []cameraResponse{}
	for rows.Next() {
		var item cameraResponse
		if err := rows.Scan(&item.ID, &item.Name, &item.Location, &item.Protocol, &item.StreamGatewayRef, &item.Status, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createCamera(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "MANAGER") {
		return
	}
	var input cameraCreateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name, input.Location, input.StreamGatewayRef = strings.TrimSpace(input.Name), strings.TrimSpace(input.Location), strings.TrimSpace(input.StreamGatewayRef)
	if !validLength(input.Name, 2, 120) || !validLength(input.Location, 2, 120) || !validLength(input.StreamGatewayRef, 2, 200) || !oneOf(input.Protocol, "RTSP", "ONVIF", "HLS", "WEBRTC") {
		writeError(w, http.StatusBadRequest, "Bad Request", "Camera details are invalid.")
		return
	}
	enabled := true
	if input.IsEnabled != nil {
		enabled = *input.IsEnabled
	}
	cameraID := uuid.NewString()
	_, err := s.db.Exec(r.Context(), `INSERT INTO "cameras" ("id","storeId","name","location","protocol","streamGatewayRef","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`, cameraID, storeID, input.Name, input.Location, input.Protocol, input.StreamGatewayRef, enabled)
	if err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "streamGatewayRef is already assigned to a camera.")
			return
		}
		s.internalError(w, err)
		return
	}
	s.writeCamera(w, r.Context(), storeID, cameraID, http.StatusCreated)
}

func (s *Server) findCamera(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID := r.PathValue("storeId"), r.PathValue("cameraId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OPERATOR") {
		return
	}
	s.writeCamera(w, r.Context(), storeID, cameraID, http.StatusOK)
}

func (s *Server) updateCamera(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID := r.PathValue("storeId"), r.PathValue("cameraId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "MANAGER") {
		return
	}
	var input cameraUpdateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name != nil && !validLength(*input.Name, 2, 120) || input.Location != nil && !validLength(*input.Location, 2, 120) || input.StreamGatewayRef != nil && !validLength(*input.StreamGatewayRef, 2, 200) || input.Protocol != nil && !oneOf(*input.Protocol, "RTSP", "ONVIF", "HLS", "WEBRTC") || input.Status != nil && !oneOf(*input.Status, "ONLINE", "OFFLINE", "DISABLED") {
		writeError(w, http.StatusBadRequest, "Bad Request", "Camera details are invalid.")
		return
	}
	query := `UPDATE "cameras" SET "updatedAt"=NOW()`
	args := []any{}
	add := func(column string, value any) {
		args = append(args, value)
		query += `, "` + column + `"=$` + itoa(len(args))
	}
	if input.Name != nil {
		add("name", strings.TrimSpace(*input.Name))
	}
	if input.Location != nil {
		add("location", strings.TrimSpace(*input.Location))
	}
	if input.Protocol != nil {
		add("protocol", *input.Protocol)
	}
	if input.StreamGatewayRef != nil {
		add("streamGatewayRef", strings.TrimSpace(*input.StreamGatewayRef))
	}
	if input.Status != nil {
		add("status", *input.Status)
	}
	if input.IsEnabled != nil {
		add("isEnabled", *input.IsEnabled)
	}
	args = append(args, cameraID, storeID)
	query += ` WHERE "id"=$` + itoa(len(args)-1) + ` AND "storeId"=$` + itoa(len(args))
	result, err := s.db.Exec(r.Context(), query, args...)
	if err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "streamGatewayRef is already assigned to a camera.")
			return
		}
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Camera was not found.")
		return
	}
	s.writeCamera(w, r.Context(), storeID, cameraID, http.StatusOK)
}

func (s *Server) removeCamera(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID := r.PathValue("storeId"), r.PathValue("cameraId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "MANAGER") {
		return
	}
	result, err := s.db.Exec(r.Context(), `DELETE FROM "cameras" WHERE "id"=$1 AND "storeId"=$2`, cameraID, storeID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Camera was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) writeCamera(w http.ResponseWriter, ctx context.Context, storeID, cameraID string, status int) {
	var item cameraResponse
	err := s.db.QueryRow(ctx, `SELECT "id","name","location","protocol","streamGatewayRef","status","isEnabled","createdAt","updatedAt" FROM "cameras" WHERE "id"=$1 AND "storeId"=$2`, cameraID, storeID).Scan(&item.ID, &item.Name, &item.Location, &item.Protocol, &item.StreamGatewayRef, &item.Status, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Camera was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, status, item)
}

func (s *Server) requireCamera(w http.ResponseWriter, ctx context.Context, userID, storeID, cameraID, role string) bool {
	if !s.requireRole(w, ctx, userID, storeID, role) {
		return false
	}
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "cameras" WHERE "id"=$1 AND "storeId"=$2)`, cameraID, storeID).Scan(&exists)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "Not Found", "Camera was not found.")
		return false
	}
	return true
}

func (s *Server) listZones(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID := r.PathValue("storeId"), r.PathValue("cameraId")
	if !s.requireCamera(w, r.Context(), user.ID, storeID, cameraID, "OPERATOR") {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT "id","name","kind","expectedPersonCategory","polygon","dwellThresholdSeconds","isEnabled" FROM "camera_zones" WHERE "cameraId"=$1 ORDER BY "name"`, cameraID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []zoneResponse{}
	for rows.Next() {
		var item zoneResponse
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.ExpectedPersonCategory, &item.Polygon, &item.DwellThresholdSeconds, &item.IsEnabled); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createZone(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID := r.PathValue("storeId"), r.PathValue("cameraId")
	if !s.requireCamera(w, r.Context(), user.ID, storeID, cameraID, "MANAGER") {
		return
	}
	var input zoneRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if !s.validZoneInput(w, input, true) {
		return
	}
	zoneID := uuid.NewString()
	var category any
	if input.ExpectedPersonCategory != nil {
		category = *input.ExpectedPersonCategory
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO "camera_zones" ("id","cameraId","name","kind","expectedPersonCategory","polygon","dwellThresholdSeconds","updatedAt") VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,NOW())`, zoneID, cameraID, strings.TrimSpace(*input.Name), *input.Kind, category, string(input.Polygon), input.DwellThresholdSeconds)
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.writeZone(w, r.Context(), cameraID, zoneID, http.StatusCreated)
}

func (s *Server) findZone(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID, zoneID := r.PathValue("storeId"), r.PathValue("cameraId"), r.PathValue("zoneId")
	if !s.requireCamera(w, r.Context(), user.ID, storeID, cameraID, "OPERATOR") {
		return
	}
	s.writeZone(w, r.Context(), cameraID, zoneID, http.StatusOK)
}

func (s *Server) updateZone(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID, zoneID := r.PathValue("storeId"), r.PathValue("cameraId"), r.PathValue("zoneId")
	if !s.requireCamera(w, r.Context(), user.ID, storeID, cameraID, "MANAGER") {
		return
	}
	var input zoneRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if !s.validZoneInput(w, input, false) {
		return
	}
	query := `UPDATE "camera_zones" SET "updatedAt"=NOW()`
	args := []any{}
	add := func(column string, value any) {
		args = append(args, value)
		query += `, "` + column + `"=$` + itoa(len(args))
	}
	if input.Name != nil {
		add("name", strings.TrimSpace(*input.Name))
	}
	if input.Kind != nil {
		add("kind", *input.Kind)
	}
	if input.ExpectedPersonCategory != nil {
		add("expectedPersonCategory", *input.ExpectedPersonCategory)
	}
	if len(input.Polygon) > 0 {
		args = append(args, string(input.Polygon))
		query += `, "polygon"=$` + itoa(len(args)) + `::jsonb`
	}
	if input.DwellThresholdSeconds != nil {
		add("dwellThresholdSeconds", *input.DwellThresholdSeconds)
	}
	args = append(args, zoneID, cameraID)
	query += ` WHERE "id"=$` + itoa(len(args)-1) + ` AND "cameraId"=$` + itoa(len(args))
	result, err := s.db.Exec(r.Context(), query, args...)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Camera zone was not found.")
		return
	}
	s.writeZone(w, r.Context(), cameraID, zoneID, http.StatusOK)
}

func (s *Server) removeZone(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, cameraID, zoneID := r.PathValue("storeId"), r.PathValue("cameraId"), r.PathValue("zoneId")
	if !s.requireCamera(w, r.Context(), user.ID, storeID, cameraID, "MANAGER") {
		return
	}
	result, err := s.db.Exec(r.Context(), `DELETE FROM "camera_zones" WHERE "id"=$1 AND "cameraId"=$2`, zoneID, cameraID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Camera zone was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) writeZone(w http.ResponseWriter, ctx context.Context, cameraID, zoneID string, status int) {
	var item zoneResponse
	err := s.db.QueryRow(ctx, `SELECT "id","name","kind","expectedPersonCategory","polygon","dwellThresholdSeconds","isEnabled" FROM "camera_zones" WHERE "id"=$1 AND "cameraId"=$2`, zoneID, cameraID).Scan(&item.ID, &item.Name, &item.Kind, &item.ExpectedPersonCategory, &item.Polygon, &item.DwellThresholdSeconds, &item.IsEnabled)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Camera zone was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, status, item)
}

func (s *Server) validZoneInput(w http.ResponseWriter, input zoneRequest, create bool) bool {
	if create && (input.Name == nil || input.Kind == nil || len(input.Polygon) == 0) {
		writeError(w, http.StatusBadRequest, "Bad Request", "name, kind, and polygon are required.")
		return false
	}
	if input.Name != nil && !validLength(*input.Name, 1, 120) || input.Kind != nil && !oneOf(*input.Kind, "HIGH_VALUE", "CASHIER", "STOCKROOM", "ENTRANCE", "CUSTOM") || input.ExpectedPersonCategory != nil && !oneOf(*input.ExpectedPersonCategory, "EMPLOYEE", "CUSTOMER", "UNKNOWN") || len(input.Polygon) > 0 && !validNormalizedPolygon(input.Polygon) || input.DwellThresholdSeconds != nil && *input.DwellThresholdSeconds < 1 {
		writeError(w, http.StatusBadRequest, "Bad Request", "Zone details are invalid. Polygon must use at least 3 normalized [x,y] points.")
		return false
	}
	return true
}

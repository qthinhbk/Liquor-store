package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var roleRank = map[string]int{"OPERATOR": 1, "MANAGER": 2, "OWNER": 3}

func (s *Server) requireRole(w http.ResponseWriter, ctx context.Context, userID, storeID, minimum string) bool {
	var role string
	err := s.db.QueryRow(ctx, `SELECT sm."role" FROM "store_memberships" sm JOIN "users" u ON u."id"=sm."userId" WHERE sm."userId"=$1 AND sm."storeId"=$2 AND u."status"='ACTIVE'`, userID, storeID).Scan(&role)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Store was not found.")
			return false
		}
		s.internalError(w, err)
		return false
	}
	if roleRank[role] < roleRank[minimum] {
		writeError(w, http.StatusForbidden, "Forbidden", "Your store role does not allow this action.")
		return false
	}
	return true
}

func (s *Server) listStores(w http.ResponseWriter, r *http.Request, user principal) {
	rows, err := s.db.Query(r.Context(), `SELECT s."id", s."name", s."code", s."address", s."timezone", sm."role" FROM "stores" s JOIN "store_memberships" sm ON sm."storeId"=s."id" WHERE sm."userId"=$1 ORDER BY s."name"`, user.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []storeResponse{}
	for rows.Next() {
		var item storeResponse
		if err := rows.Scan(&item.ID, &item.Name, &item.Code, &item.Address, &item.Timezone, &item.Role); err != nil {
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

func (s *Server) createStore(w http.ResponseWriter, r *http.Request, user principal) {
	var input struct {
		Name    string  `json:"name"`
		Code    string  `json:"code"`
		Address *string `json:"address"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name, input.Code = strings.TrimSpace(input.Name), strings.TrimSpace(input.Code)
	if !validLength(input.Name, 2, 120) || !storeCodePattern.MatchString(input.Code) || (input.Address != nil && !validLength(*input.Address, 0, 500)) {
		writeError(w, http.StatusBadRequest, "Bad Request", "Store details are invalid.")
		return
	}
	storeID := uuid.NewString()
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var address any
	if input.Address != nil {
		address = strings.TrimSpace(*input.Address)
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO "stores" ("id","name","code","address","updatedAt") VALUES ($1,$2,$3,$4,NOW())`, storeID, input.Name, input.Code, address); err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This store code is already in use.")
			return
		}
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO "store_memberships" ("id","userId","storeId","role","updatedAt") VALUES ($1,$2,$3,'OWNER',NOW())`, uuid.NewString(), user.ID, storeID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	s.writeStore(w, r.Context(), storeID, http.StatusCreated)
}

func (s *Server) findStore(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OPERATOR") {
		return
	}
	s.writeStore(w, r.Context(), storeID, http.StatusOK)
}

func (s *Server) updateStore(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input struct {
		Name     *string `json:"name"`
		Address  *string `json:"address"`
		Timezone *string `json:"timezone"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name != nil && !validLength(*input.Name, 2, 120) {
		writeError(w, http.StatusBadRequest, "Bad Request", "name must contain 2 to 120 characters.")
		return
	}
	if input.Address != nil && !validLength(*input.Address, 0, 500) {
		writeError(w, http.StatusBadRequest, "Bad Request", "address must be at most 500 characters.")
		return
	}
	if input.Timezone != nil && !validLength(*input.Timezone, 1, 64) {
		writeError(w, http.StatusBadRequest, "Bad Request", "timezone must be at most 64 characters.")
		return
	}
	query := `UPDATE "stores" SET "updatedAt"=NOW()`
	args := []any{}
	if input.Name != nil {
		args = append(args, strings.TrimSpace(*input.Name))
		query += `, "name"=$` + itoa(len(args))
	}
	if input.Address != nil {
		args = append(args, strings.TrimSpace(*input.Address))
		query += `, "address"=$` + itoa(len(args))
	}
	if input.Timezone != nil {
		args = append(args, strings.TrimSpace(*input.Timezone))
		query += `, "timezone"=$` + itoa(len(args))
	}
	args = append(args, storeID)
	query += ` WHERE "id"=$` + itoa(len(args))
	if _, err := s.db.Exec(r.Context(), query, args...); err != nil {
		s.internalError(w, err)
		return
	}
	s.writeStore(w, r.Context(), storeID, http.StatusOK)
}

func (s *Server) writeStore(w http.ResponseWriter, ctx context.Context, storeID string, status int) {
	var item storeResponse
	err := s.db.QueryRow(ctx, `SELECT "id", "name", "code", "address", "timezone", "createdAt", "updatedAt" FROM "stores" WHERE "id"=$1`, storeID).
		Scan(&item.ID, &item.Name, &item.Code, &item.Address, &item.Timezone, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Store was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, status, item)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "MANAGER") {
		return
	}
	type memberResponse struct {
		ID          string    `json:"id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"displayName"`
		Status      string    `json:"status"`
		Role        string    `json:"role"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	rows, err := s.db.Query(r.Context(), `SELECT u."id",u."email",u."displayName",u."status",sm."role",sm."createdAt" FROM "store_memberships" sm JOIN "users" u ON u."id"=sm."userId" WHERE sm."storeId"=$1 ORDER BY sm."role",u."displayName"`, storeID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []memberResponse{}
	for rows.Next() {
		var item memberResponse
		if err := rows.Scan(&item.ID, &item.Email, &item.DisplayName, &item.Status, &item.Role, &item.CreatedAt); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) addMember(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(input.Email) || !oneOf(input.Role, "MANAGER", "OPERATOR") {
		writeError(w, http.StatusBadRequest, "Bad Request", "Member details are invalid.")
		return
	}
	var memberID string
	if err := s.db.QueryRow(r.Context(), `SELECT "id" FROM "users" WHERE "email"=$1`, input.Email).Scan(&memberID); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "The invited user must register first.")
			return
		}
		s.internalError(w, err)
		return
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO "store_memberships" ("id","userId","storeId","role","updatedAt") VALUES ($1,$2,$3,$4,NOW())`, uuid.NewString(), memberID, storeID, input.Role)
	if err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This user already belongs to the store.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"userId": memberID, "role": input.Role})
}

func (s *Server) updateMember(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, memberID := r.PathValue("storeId"), r.PathValue("memberId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !oneOf(input.Role, "MANAGER", "OPERATOR") {
		writeError(w, http.StatusBadRequest, "Bad Request", "role must be MANAGER or OPERATOR.")
		return
	}
	if !s.memberCanChange(w, r.Context(), storeID, memberID) {
		return
	}
	var response struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	err := s.db.QueryRow(r.Context(), `UPDATE "store_memberships" SET "role"=$1,"updatedAt"=NOW() WHERE "storeId"=$2 AND "userId"=$3 RETURNING "userId","role"`, input.Role, storeID, memberID).Scan(&response.UserID, &response.Role)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Store member was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, memberID := r.PathValue("storeId"), r.PathValue("memberId")
	if !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	if memberID == user.ID {
		writeError(w, http.StatusConflict, "Conflict", "An owner cannot remove themselves.")
		return
	}
	if !s.memberCanChange(w, r.Context(), storeID, memberID) {
		return
	}
	result, err := s.db.Exec(r.Context(), `DELETE FROM "store_memberships" WHERE "storeId"=$1 AND "userId"=$2`, storeID, memberID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Store member was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) memberCanChange(w http.ResponseWriter, ctx context.Context, storeID, memberID string) bool {
	var role string
	err := s.db.QueryRow(ctx, `SELECT "role" FROM "store_memberships" WHERE "storeId"=$1 AND "userId"=$2`, storeID, memberID).Scan(&role)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Store member was not found.")
			return false
		}
		s.internalError(w, err)
		return false
	}
	if role == "OWNER" {
		writeError(w, http.StatusConflict, "Conflict", "Owner membership cannot be changed or removed.")
		return false
	}
	return true
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

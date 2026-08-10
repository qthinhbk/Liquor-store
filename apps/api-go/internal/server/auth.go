package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/liquor-store/security-api/internal/security"
)

// #nosec G101 -- this public test hash is used only to equalize failed-login timing and cannot authenticate any account.
const dummyPasswordHash = "$argon2id$v=19$m=65536,p=4,t=3$tRJklxOdZO9aABYGMhSuPQ$efdf+S6xCWV0Qd61ZJlhMSYxZUR4weZFzfXrlb2FE9A"

type registerRequest struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	DisplayName string  `json:"displayName"`
	StoreName   string  `json:"storeName"`
	StoreCode   string  `json:"storeCode"`
	Address     *string `json:"address"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionResponse struct {
	AccessToken string      `json:"accessToken"`
	User        currentUser `json:"user"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var input registerRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.StoreName = strings.TrimSpace(input.StoreName)
	input.StoreCode = strings.TrimSpace(input.StoreCode)
	if !validEmail(input.Email) || len(input.Password) < 12 || len(input.Password) > 128 ||
		!validLength(input.DisplayName, 2, 100) || !validLength(input.StoreName, 2, 120) || !storeCodePattern.MatchString(input.StoreCode) ||
		(input.Address != nil && !validLength(*input.Address, 0, 500)) {
		writeError(w, http.StatusBadRequest, "Bad Request", "Registration details are invalid.")
		return
	}

	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		s.internalError(w, err)
		return
	}
	user := authUser{ID: uuid.NewString(), Email: input.Email, DisplayName: input.DisplayName, PasswordHash: passwordHash, Status: "ACTIVE"}
	storeID := uuid.NewString()
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err = tx.Exec(r.Context(), `INSERT INTO "users" ("id", "email", "passwordHash", "displayName", "status", "updatedAt") VALUES ($1,$2,$3,$4,'ACTIVE',NOW())`, user.ID, user.Email, passwordHash, user.DisplayName); err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "Email or store code is already in use.")
			return
		}
		s.internalError(w, err)
		return
	}
	var address any
	if input.Address != nil {
		trimmed := strings.TrimSpace(*input.Address)
		address = trimmed
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO "stores" ("id", "name", "code", "address", "updatedAt") VALUES ($1,$2,$3,$4,NOW())`, storeID, input.StoreName, input.StoreCode, address); err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "Email or store code is already in use.")
			return
		}
		s.internalError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO "store_memberships" ("id", "userId", "storeId", "role", "updatedAt") VALUES ($1,$2,$3,'OWNER',NOW())`, uuid.NewString(), user.ID, storeID); err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}

	session, refreshToken, err := s.createSession(r.Context(), user, r)
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.setRefreshCookie(w, refreshToken)
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input loginRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(input.Email) || len(input.Password) < 1 || len(input.Password) > 128 {
		writeError(w, http.StatusBadRequest, "Bad Request", "Email or password is invalid.")
		return
	}
	var user authUser
	err := s.db.QueryRow(r.Context(), `SELECT "id", "email", "displayName", "passwordHash", "status" FROM "users" WHERE "email"=$1`, input.Email).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash, &user.Status)
	if err != nil {
		if err == pgx.ErrNoRows {
			_, _ = security.VerifyPassword(input.Password, dummyPasswordHash)
			writeError(w, http.StatusUnauthorized, "Unauthorized", "Invalid email or password.")
			return
		}
		s.internalError(w, err)
		return
	}
	verified, verifyErr := security.VerifyPassword(input.Password, user.PasswordHash)
	if verifyErr != nil || !verified || user.Status != "ACTIVE" {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Invalid email or password.")
		return
	}
	session, refreshToken, err := s.createSession(r.Context(), user, r)
	if err != nil {
		s.internalError(w, err)
		return
	}
	s.setRefreshCookie(w, refreshToken)
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Refresh token is missing.")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var userID string
	err = tx.QueryRow(r.Context(), `UPDATE "refresh_sessions" SET "revokedAt"=NOW(), "updatedAt"=NOW()
		WHERE "tokenHash"=$1 AND "revokedAt" IS NULL AND "expiresAt">NOW() RETURNING "userId"`, hashRefreshToken(cookie.Value)).Scan(&userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Refresh token is invalid or expired.")
		return
	}
	var user authUser
	err = tx.QueryRow(r.Context(), `SELECT "id", "email", "displayName", "passwordHash", "status" FROM "users" WHERE "id"=$1 AND "status"='ACTIVE'`, userID).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash, &user.Status)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Refresh token is invalid or expired.")
		return
	}
	session, refreshToken, err := s.createSessionWith(r.Context(), tx, user, r)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	s.setRefreshCookie(w, refreshToken)
	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("refresh_token"); err == nil && cookie.Value != "" {
		_, _ = s.db.Exec(r.Context(), `UPDATE "refresh_sessions" SET "revokedAt"=NOW(), "updatedAt"=NOW() WHERE "tokenHash"=$1`, hashRefreshToken(cookie.Value))
	}
	// #nosec G124 -- Secure is mandatory in production; localhost development intentionally uses HTTP.
	http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "", Path: "/api/v1/auth", MaxAge: -1, HttpOnly: true, Secure: s.config.Environment == "production", SameSite: s.cookieSameSite()})
	writeJSON(w, http.StatusCreated, map[string]bool{"success": true})
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request, userPrincipal principal) {
	user, err := s.loadCurrentUser(r.Context(), userPrincipal.ID)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusUnauthorized, "Unauthorized", "User is not active.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) userProfile(w http.ResponseWriter, r *http.Request, user principal) {
	var response struct {
		ID          string    `json:"id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"displayName"`
		Status      string    `json:"status"`
		CreatedAt   time.Time `json:"createdAt"`
	}
	err := s.db.QueryRow(r.Context(), `SELECT "id", "email", "displayName", "status", "createdAt" FROM "users" WHERE "id"=$1`, user.ID).
		Scan(&response.ID, &response.Email, &response.DisplayName, &response.Status, &response.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "User was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) updateUserProfile(w http.ResponseWriter, r *http.Request, user principal) {
	var input struct {
		DisplayName string `json:"displayName"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if !validLength(input.DisplayName, 2, 100) {
		writeError(w, http.StatusBadRequest, "Bad Request", "displayName must contain 2 to 100 characters.")
		return
	}
	var response struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	err := s.db.QueryRow(r.Context(), `UPDATE "users" SET "displayName"=$1, "updatedAt"=NOW() WHERE "id"=$2 RETURNING "id", "email", "displayName"`, input.DisplayName, user.ID).
		Scan(&response.ID, &response.Email, &response.DisplayName)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "User was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type sessionStore interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Server) createSession(ctx context.Context, user authUser, r *http.Request) (sessionResponse, string, error) {
	return s.createSessionWith(ctx, s.db, user, r)
}

func (s *Server) createSessionWith(ctx context.Context, store sessionStore, user authUser, r *http.Request) (sessionResponse, string, error) {
	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return sessionResponse{}, "", err
	}
	refreshToken := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := time.Now().Add(time.Duration(s.config.RefreshTTLDays) * 24 * time.Hour)
	userAgent := strings.TrimSpace(r.UserAgent())
	if len(userAgent) > 500 {
		userAgent = userAgent[:500]
	}
	if _, err := store.Exec(ctx, `INSERT INTO "refresh_sessions" ("id", "userId", "tokenHash", "expiresAt", "ipAddress", "userAgent", "updatedAt") VALUES ($1,$2,$3,$4,$5,$6,NOW())`, uuid.NewString(), user.ID, hashRefreshToken(refreshToken), expiresAt, s.clientIP(r), userAgent); err != nil {
		return sessionResponse{}, "", err
	}
	accessToken, err := s.signAccessToken(user)
	if err != nil {
		return sessionResponse{}, "", err
	}
	current, err := s.loadCurrentUserWith(ctx, store, user.ID)
	if err != nil {
		return sessionResponse{}, "", err
	}
	return sessionResponse{AccessToken: accessToken, User: current}, refreshToken, nil
}

func (s *Server) loadCurrentUser(ctx context.Context, userID string) (currentUser, error) {
	return s.loadCurrentUserWith(ctx, s.db, userID)
}

func (s *Server) loadCurrentUserWith(ctx context.Context, store sessionStore, userID string) (currentUser, error) {
	var user currentUser
	var status string
	if err := store.QueryRow(ctx, `SELECT "id", "email", "displayName", "status" FROM "users" WHERE "id"=$1 AND "status"='ACTIVE'`, userID).Scan(&user.ID, &user.Email, &user.DisplayName, &status); err != nil {
		return currentUser{}, err
	}
	rows, err := store.Query(ctx, `SELECT sm."storeId", s."name", s."code", sm."role" FROM "store_memberships" sm JOIN "stores" s ON s."id"=sm."storeId" WHERE sm."userId"=$1 ORDER BY s."name"`, userID)
	if err != nil {
		return currentUser{}, err
	}
	defer rows.Close()
	user.Stores = []membership{}
	for rows.Next() {
		var item membership
		if err := rows.Scan(&item.StoreID, &item.StoreName, &item.StoreCode, &item.Role); err != nil {
			return currentUser{}, err
		}
		user.Stores = append(user.Stores, item)
	}
	return user, rows.Err()
}

func (s *Server) setRefreshCookie(w http.ResponseWriter, token string) {
	maxAge := s.config.RefreshTTLDays * 24 * 60 * 60
	// #nosec G124 -- Secure is mandatory in production; localhost development intentionally uses HTTP.
	http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: token, Path: "/api/v1/auth", MaxAge: maxAge, Expires: time.Now().Add(time.Duration(maxAge) * time.Second), HttpOnly: true, Secure: s.config.Environment == "production", SameSite: s.cookieSameSite()})
}

func (s *Server) cookieSameSite() http.SameSite {
	if s.config.Environment == "production" {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

func hashRefreshToken(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "Internal Server Error", "An unexpected error occurred.")
}

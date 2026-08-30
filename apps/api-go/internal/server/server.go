package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liquor-store/security-api/internal/config"
	"github.com/liquor-store/security-api/internal/notifications"
)

type Server struct {
	db     *pgxpool.Pool
	config config.Config
	log    *slog.Logger
	limits *rateLimitStore
	review *notifications.SecureReviewService
}

type principal struct {
	ID    string
	Email string
}

type contextKey string

const principalKey contextKey = "principal"

type accessClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

func New(cfg config.Config, db *pgxpool.Pool, logger *slog.Logger) *Server {
	return &Server{db: db, config: cfg, log: logger, limits: newRateLimitStore(10_000)}
}

func (s *Server) SetSecureReviewService(review *notifications.SecureReviewService) {
	s.review = review
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/notification-review/{token}", s.reviewNotificationEvidence)
	mux.HandleFunc("GET /api/v1/webhooks/whatsapp", s.verifyWhatsAppWebhook)
	mux.HandleFunc("POST /api/v1/webhooks/whatsapp", s.receiveWhatsAppWebhook)
	if s.config.AIIngestToken != "" {
		mux.Handle("POST /api/v1/internal/ai/alerts", s.aiIngestEndpoint(http.HandlerFunc(s.ingestAIAlert)))
	}
	if s.config.RegisterEnabled {
		mux.Handle("POST /api/v1/auth/register", s.authEndpoint("register", 3, time.Hour, http.HandlerFunc(s.register)))
	}
	mux.Handle("POST /api/v1/auth/login", s.authEndpoint("login", 10, time.Minute, http.HandlerFunc(s.login)))
	mux.Handle("POST /api/v1/auth/refresh", s.authEndpoint("refresh", 30, time.Minute, http.HandlerFunc(s.refresh)))
	mux.Handle("POST /api/v1/auth/logout", s.authEndpoint("logout", 30, time.Minute, http.HandlerFunc(s.logout)))
	if s.config.SwaggerEnabled {
		mux.HandleFunc("GET /docs-json", s.openAPIJSON)
		mux.HandleFunc("GET /docs", s.swaggerUI)
		mux.HandleFunc("GET /docs/", s.swaggerUI)
	}

	s.protected(mux, "GET /api/v1/auth/me", s.authMe)
	s.protected(mux, "GET /api/v1/users/me", s.userProfile)
	s.protected(mux, "PATCH /api/v1/users/me", s.updateUserProfile)

	s.protected(mux, "GET /api/v1/stores", s.listStores)
	s.protected(mux, "POST /api/v1/stores", s.createStore)
	s.protected(mux, "GET /api/v1/stores/{storeId}", s.findStore)
	s.protected(mux, "PATCH /api/v1/stores/{storeId}", s.updateStore)
	if s.config.MembersEnabled {
		s.protected(mux, "GET /api/v1/stores/{storeId}/members", s.listMembers)
		s.protected(mux, "POST /api/v1/stores/{storeId}/members", s.addMember)
		s.protected(mux, "PATCH /api/v1/stores/{storeId}/members/{memberId}", s.updateMember)
		s.protected(mux, "DELETE /api/v1/stores/{storeId}/members/{memberId}", s.removeMember)
	}

	s.protected(mux, "GET /api/v1/stores/{storeId}/cameras", s.listCameras)
	s.protected(mux, "POST /api/v1/stores/{storeId}/cameras", s.createCamera)
	s.protected(mux, "GET /api/v1/stores/{storeId}/cameras/{cameraId}", s.findCamera)
	s.protected(mux, "PATCH /api/v1/stores/{storeId}/cameras/{cameraId}", s.updateCamera)
	s.protected(mux, "DELETE /api/v1/stores/{storeId}/cameras/{cameraId}", s.removeCamera)
	s.protected(mux, "GET /api/v1/stores/{storeId}/cameras/{cameraId}/zones", s.listZones)
	s.protected(mux, "POST /api/v1/stores/{storeId}/cameras/{cameraId}/zones", s.createZone)
	s.protected(mux, "GET /api/v1/stores/{storeId}/cameras/{cameraId}/zones/{zoneId}", s.findZone)
	s.protected(mux, "PATCH /api/v1/stores/{storeId}/cameras/{cameraId}/zones/{zoneId}", s.updateZone)
	s.protected(mux, "DELETE /api/v1/stores/{storeId}/cameras/{cameraId}/zones/{zoneId}", s.removeZone)

	s.protected(mux, "GET /api/v1/stores/{storeId}/alerts", s.listAlerts)
	s.protected(mux, "GET /api/v1/stores/{storeId}/alerts/{alertId}", s.findAlert)
	s.protected(mux, "POST /api/v1/stores/{storeId}/alerts/{alertId}/acknowledge", s.acknowledgeAlert)
	s.protected(mux, "POST /api/v1/stores/{storeId}/alerts/{alertId}/dismiss", s.dismissAlert)
	s.protected(mux, "POST /api/v1/stores/{storeId}/alerts/{alertId}/resolve", s.resolveAlert)
	s.protected(mux, "GET /api/v1/stores/{storeId}/alerts/{alertId}/evidence/{evidenceId}/playback-url", s.createEvidencePlaybackURL)

	s.protected(mux, "GET /api/v1/stores/{storeId}/notification-endpoints", s.listNotificationEndpoints)
	s.protected(mux, "POST /api/v1/stores/{storeId}/notification-endpoints", s.createNotificationEndpoint)
	s.protected(mux, "PATCH /api/v1/stores/{storeId}/notification-endpoints/{endpointId}", s.updateNotificationEndpoint)
	s.protected(mux, "DELETE /api/v1/stores/{storeId}/notification-endpoints/{endpointId}", s.removeNotificationEndpoint)
	s.protected(mux, "POST /api/v1/stores/{storeId}/notification-endpoints/{endpointId}/test", s.testNotificationEndpoint)
	s.protected(mux, "GET /api/v1/stores/{storeId}/notification-rules", s.listNotificationRules)
	s.protected(mux, "POST /api/v1/stores/{storeId}/notification-rules", s.createNotificationRule)
	s.protected(mux, "PATCH /api/v1/stores/{storeId}/notification-rules/{ruleId}", s.updateNotificationRule)
	s.protected(mux, "DELETE /api/v1/stores/{storeId}/notification-rules/{ruleId}", s.removeNotificationRule)
	s.protected(mux, "GET /api/v1/stores/{storeId}/notification-rules/{ruleId}/channels", s.listNotificationRuleChannels)
	s.protected(mux, "POST /api/v1/stores/{storeId}/notification-rules/{ruleId}/channels", s.createNotificationRuleChannel)
	s.protected(mux, "PATCH /api/v1/stores/{storeId}/notification-rules/{ruleId}/channels/{channelId}", s.updateNotificationRuleChannel)
	s.protected(mux, "DELETE /api/v1/stores/{storeId}/notification-rules/{ruleId}/channels/{channelId}", s.removeNotificationRuleChannel)
	s.protected(mux, "GET /api/v1/stores/{storeId}/notification-deliveries", s.listNotificationDeliveries)
	s.protected(mux, "GET /api/v1/stores/{storeId}/notification-deliveries/{deliveryId}", s.findNotificationDelivery)

	return s.recoverer(s.securityHeaders(s.cors(s.requestLog(mux))))
}

func (s *Server) protected(mux *http.ServeMux, pattern string, handler func(http.ResponseWriter, *http.Request, principal)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Unauthorized", err.Error())
			return
		}
		handler(w, r.WithContext(context.WithValue(r.Context(), principalKey, user)), user)
	})
}

func (s *Server) authenticate(r *http.Request) (principal, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return principal{}, errors.New("Authentication is required.")
	}
	tokenString := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	claims := &accessClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected JWT signing method")
		}
		return []byte(s.config.JWTAccessSecret), nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithIssuer(s.config.JWTIssuer), jwt.WithAudience(s.config.JWTAudience))
	if err != nil || !token.Valid || claims.Subject == "" {
		return principal{}, errors.New("Access token is invalid or expired.")
	}
	return principal{ID: claims.Subject, Email: claims.Email}, nil
}

func (s *Server) signAccessToken(user authUser) (string, error) {
	now := time.Now()
	claims := accessClaims{
		Email: user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			Issuer:    s.config.JWTIssuer,
			Audience:  jwt.ClaimStrings{s.config.JWTAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.config.JWTAccessTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.config.JWTAccessSecret))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "liquor-store-security-api"})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == s.config.WebOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Pragma", "no-cache")
		}
		if s.config.Environment == "production" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.log.Info("request", "method", r.Method, "path", safeRequestPath(r.URL.Path), "duration", time.Since(started))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("panic recovered", "error", recovered, "path", safeRequestPath(r.URL.Path))
				writeError(w, http.StatusInternalServerError, "Internal Server Error", "An unexpected error occurred.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func safeRequestPath(path string) string {
	if strings.HasPrefix(path, "/api/v1/notification-review/") {
		return "/api/v1/notification-review/[redacted]"
	}
	return path
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, label, message string) {
	writeJSON(w, status, map[string]any{"statusCode": status, "error": label, "message": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request", fmt.Sprintf("Invalid request body: %v", err))
		return false
	}
	if decoder.Decode(&struct{}{}) == nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "Request body must contain one JSON object.")
		return false
	}
	return true
}

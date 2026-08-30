package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liquor-store/security-api/internal/config"
)

func newTestHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Config{
		JWTAccessSecret: "test-secret",
		JWTIssuer:       "test-issuer",
		JWTAudience:     "test-audience",
		SwaggerEnabled:  true,
	}, nil, logger).Handler()
}

func TestAuthRateLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(config.Config{WebOrigin: "https://dashboard.example"}, nil, logger)
	handler := s.authEndpoint("login", 2, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for attempt := 1; attempt <= 3; attempt++ {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		request.RemoteAddr = "192.0.2.1:1234"
		handler.ServeHTTP(recorder, request)
		expected := http.StatusNoContent
		if attempt == 3 {
			expected = http.StatusTooManyRequests
		}
		if recorder.Code != expected {
			t.Fatalf("attempt %d: expected %d, got %d", attempt, expected, recorder.Code)
		}
	}
}

func TestProductionAuthRejectsMissingOrUntrustedOrigin(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(config.Config{Environment: "production", WebOrigin: "https://dashboard.example"}, nil, logger)
	handler := s.authEndpoint("refresh", 10, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, origin := range []string{"", "https://attacker.example"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("origin %q: expected 403, got %d", origin, recorder.Code)
		}
	}
}

func TestAuthResponsesDisableCaching(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	newTestHandler().ServeHTTP(recorder, request)

	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", recorder.Header().Get("Cache-Control"))
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("expected a Content-Security-Policy header")
	}
}

func TestRegistrationAndMemberManagementAreDisabledByDefault(t *testing.T) {
	for _, testCase := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/v1/auth/register"},
		{method: http.MethodGet, path: "/api/v1/stores/00000000-0000-0000-0000-000000000000/members"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(testCase.method, testCase.path, nil)
		newTestHandler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s: expected 404, got %d", testCase.method, testCase.path, recorder.Code)
		}
	}
}

func TestJWTRequiresExpectedIssuerAndAudience(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		JWTAccessSecret: "a-test-secret-that-is-long-enough-for-tests",
		JWTAccessTTL:    15 * time.Minute,
		JWTIssuer:       "expected-issuer",
		JWTAudience:     "expected-audience",
	}
	s := New(cfg, nil, logger)
	token, err := s.signAccessToken(authUser{ID: "user-id", Email: "owner@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stores", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, err := s.authenticate(request); err != nil {
		t.Fatalf("valid token was rejected: %v", err)
	}

	cfg.JWTAudience = "different-audience"
	if _, err := New(cfg, nil, logger).authenticate(request); err == nil {
		t.Fatal("token with the wrong audience was accepted")
	}
}

func TestProductionRefreshCookieIsHardened(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(config.Config{Environment: "production", RefreshTTLDays: 30}, nil, logger)
	recorder := httptest.NewRecorder()
	s.setRefreshCookie(recorder, "test-token")
	cookie := recorder.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteNoneMode {
		t.Fatalf("unexpected production cookie attributes: HttpOnly=%v Secure=%v SameSite=%v", cookie.HttpOnly, cookie.Secure, cookie.SameSite)
	}
}

func TestHealth(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	newTestHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
}

func TestProtectedRouteRejectsMissingToken(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/stores", nil)
	newTestHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestSafeRequestPathRedactsReviewBearerToken(t *testing.T) {
	token := "secret-review-token-that-must-never-enter-logs"
	got := safeRequestPath("/api/v1/notification-review/" + token)
	if got != "/api/v1/notification-review/[redacted]" {
		t.Fatalf("review path was not redacted: %q", got)
	}
	if got := safeRequestPath("/api/v1/health"); got != "/api/v1/health" {
		t.Fatalf("ordinary path changed: %q", got)
	}
}

func TestOpenAPIDocumentIsValidJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/docs-json", nil)
	newTestHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var document map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if document["openapi"] == nil {
		t.Fatal("OpenAPI version is missing")
	}
}

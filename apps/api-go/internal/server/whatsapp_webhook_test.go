package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liquor-store/security-api/internal/config"
)

func newWhatsAppWebhookTestHandler() http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(config.Config{
		WhatsAppWebhookVerifyToken: "verify-token",
		WhatsAppAppSecret:          "app-secret",
	}, nil, logger).Handler()
}

func TestWhatsAppWebhookVerification(t *testing.T) {
	handler := newWhatsAppWebhookTestHandler()
	valid := httptest.NewRecorder()
	handler.ServeHTTP(valid, httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=verify-token&hub.challenge=123456", nil))
	if valid.Code != http.StatusOK || valid.Body.String() != "123456" {
		t.Fatalf("valid verification = %d %q", valid.Code, valid.Body.String())
	}
	if valid.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("webhook verification response must not be cached")
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=123456", nil))
	if invalid.Code != http.StatusForbidden {
		t.Fatalf("invalid verification returned %d", invalid.Code)
	}
	if strings.Contains(invalid.Body.String(), "verify-token") {
		t.Fatal("verification token leaked into response")
	}
}

func TestWhatsAppWebhookRejectsInvalidSignatureBeforeDatabase(t *testing.T) {
	body := `{"object":"whatsapp_business_account","entry":[]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/whatsapp", strings.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	newWhatsAppWebhookTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature returned %d", recorder.Code)
	}
}

func TestWhatsAppWebhookRejectsMalformedSignedPayloadBeforeDatabase(t *testing.T) {
	body := []byte(`{"object":`)
	mac := hmac.New(sha256.New, []byte("app-secret"))
	_, _ = mac.Write(body)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/whatsapp", strings.NewReader(string(body)))
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	newWhatsAppWebhookTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("malformed signed payload returned %d", recorder.Code)
	}
}

func TestWhatsAppWebhookAcceptsSignedPayloadWithoutStatusEvents(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	mac := hmac.New(sha256.New, []byte("app-secret"))
	_, _ = mac.Write(body)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/whatsapp", strings.NewReader(string(body)))
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	newWhatsAppWebhookTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("signed webhook without receipt events returned %d", recorder.Code)
	}
}

func TestWhatsAppWebhookFailsClosedWhenUnconfigured(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=x&hub.challenge=1", nil)
	newTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured webhook returned %d", recorder.Code)
	}
}

package config

import (
	"strings"
	"testing"
)

func setMinimumTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("NODE_ENV", "development")
	t.Setenv("DATABASE_URL", "postgresql://user:password@example.test/database")
	t.Setenv("DIRECT_URL", "")
	t.Setenv("AI_INGEST_TOKEN", "")
	t.Setenv("NOTIFICATION_CREDENTIAL_BINDINGS", "")
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	t.Setenv("NOTIFICATION_WORKER_ENABLED", "false")
	t.Setenv("PUBLIC_API_BASE_URL", "")
	t.Setenv("EVIDENCE_ORIGIN_BASE_URL", "")
	t.Setenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN", "")
	t.Setenv("WHATSAPP_APP_SECRET", "")
}

func TestAIIngestTokenLengthValidation(t *testing.T) {
	setMinimumTestEnvironment(t)
	t.Setenv("AI_INGEST_TOKEN", "too-short")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AI_INGEST_TOKEN") {
		t.Fatalf("expected short AI token to fail, got %v", err)
	}
	t.Setenv("AI_INGEST_TOKEN", "a-production-style-ai-ingest-token-value")
	if _, err := Load(); err != nil {
		t.Fatalf("valid AI token rejected: %v", err)
	}
}

func TestNotificationWorkerRequiresSecureReviewOrigins(t *testing.T) {
	setMinimumTestEnvironment(t)
	t.Setenv("NOTIFICATION_WORKER_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PUBLIC_API_BASE_URL") {
		t.Fatalf("expected missing review origins to fail, got %v", err)
	}
	t.Setenv("PUBLIC_API_BASE_URL", "https://api.example.test")
	t.Setenv("EVIDENCE_ORIGIN_BASE_URL", "https://evidence.example.test")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "NOTIFICATION_CREDENTIAL_BINDINGS") {
		t.Fatalf("unscoped worker must fail to start: %v", err)
	}
	t.Setenv("NOTIFICATION_CREDENTIAL_BINDINGS", `[{"storeId":"11111111-1111-4111-8111-111111111111","provider":"TELEGRAM","providerAccountRef":"","credentialRef":"env://TELEGRAM_BOT_TOKEN"}]`)
	if _, err := Load(); err != nil {
		t.Fatalf("valid worker origins rejected: %v", err)
	}
}

func TestWhatsAppWebhookSecretsMustBePaired(t *testing.T) {
	setMinimumTestEnvironment(t)
	t.Setenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN", "verify-token")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("expected unpaired webhook secret to fail, got %v", err)
	}
}

func TestTrustedProxyCIDRConfiguration(t *testing.T) {
	setMinimumTestEnvironment(t)
	for _, raw := range []string{"*", "0.0.0.0/0", "::/0", "10.0.0.1", "10.0.0.0/24,"} {
		t.Setenv("TRUSTED_PROXY_CIDRS", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("unsafe proxy config accepted: %q", raw)
		}
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/24, 2001:db8:1::/64")
	cfg, err := Load()
	if err != nil || len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("valid proxy config rejected: %v", err)
	}
}

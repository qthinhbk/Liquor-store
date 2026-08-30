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

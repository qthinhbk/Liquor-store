package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/liquor-store/security-api/internal/config"
)

func TestProductionNotificationDatabaseReadiness(t *testing.T) {
	if os.Getenv("RUN_PRODUCTION_READINESS") != "1" {
		t.Skip("set RUN_PRODUCTION_READINESS=1 for a read-only Neon readiness check")
	}
	_ = godotenv.Load("../../.env")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	var latestMigration string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max("version"),'') FROM "go_schema_migrations"`).Scan(&latestMigration); err != nil {
		t.Fatalf("read migration state: %v", err)
	}
	var telegramEndpoints, whatsAppEndpoints, invalidCredentialRefs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE "provider"='TELEGRAM' AND "isEnabled"),count(*) FILTER (WHERE "provider"='WHATSAPP' AND "isEnabled"),count(*) FILTER (WHERE "isEnabled" AND "credentialRef" NOT LIKE 'env://%') FROM "notification_endpoints"`).Scan(&telegramEndpoints, &whatsAppEndpoints, &invalidCredentialRefs); err != nil {
		t.Fatalf("read endpoint readiness: %v", err)
	}
	var emergencyRules, enabledChannels, pendingDeliveries int
	if err := pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM "notification_rules" WHERE "isEnabled" AND "minimumSeverity"='CRITICAL' AND ('WEAPON_DETECTED'=ANY("alertTypes") OR cardinality("alertTypes")=0)),
  (SELECT count(*) FROM "notification_rule_channels" rc JOIN "notification_rules" r ON r."id"=rc."ruleId" AND r."storeId"=rc."storeId" JOIN "notification_endpoints" e ON e."id"=rc."endpointId" AND e."storeId"=rc."storeId" WHERE rc."isEnabled" AND r."isEnabled" AND e."isEnabled"),
  (SELECT count(*) FROM "notification_deliveries" WHERE "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED'))`).Scan(&emergencyRules, &enabledChannels, &pendingDeliveries); err != nil {
		t.Fatalf("read routing readiness: %v", err)
	}

	t.Logf("readiness: latestMigration=%s telegramEndpoints=%d whatsAppEndpoints=%d emergencyRules=%d enabledChannels=%d queued=%d", latestMigration, telegramEndpoints, whatsAppEndpoints, emergencyRules, enabledChannels, pendingDeliveries)
	if latestMigration != "20260829000000_notification_provider_receipts" {
		t.Errorf("latest migration is %q", latestMigration)
	}
	if telegramEndpoints < 1 || whatsAppEndpoints < 1 {
		t.Errorf("production routing needs at least one enabled Telegram and WhatsApp endpoint")
	}
	if emergencyRules < 1 || enabledChannels < 2 {
		t.Errorf("production routing needs an enabled CRITICAL weapon rule with primary and fallback channels")
	}
	if invalidCredentialRefs != 0 {
		t.Errorf("%d enabled endpoints use a credentialRef unsupported by the runtime resolver", invalidCredentialRefs)
	}
}

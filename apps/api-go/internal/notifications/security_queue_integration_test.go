package notifications

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSecurityQueuePrioritizesAlerts(t *testing.T) {
	if os.Getenv("RUN_NOTIFICATION_RUNTIME_INTEGRATION_TESTS") != "1" {
		t.Skip("requires disposable PostgreSQL")
	}
	dsn := strings.TrimSpace(os.Getenv("NOTIFICATION_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Fatal("NOTIFICATION_TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	storeID, endpointID, alertID, ruleID, channelID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	defer func() {
		_, _ = db.Exec(ctx, `DELETE FROM "notification_deliveries" WHERE "storeId"=$1`, storeID)
		_, _ = db.Exec(ctx, `DELETE FROM "stores" WHERE "id"=$1`, storeID)
	}()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO "stores" ("id","name","code","updatedAt") VALUES ($1,'Queue test',$2,NOW())`, storeID, storeID)
	exec(`INSERT INTO "notification_endpoints" ("id","storeId","provider","label","destinationRef","credentialRef","config","updatedAt") VALUES ($1,$2,'TELEGRAM','Test','test-only','env://TELEGRAM_BOT_TOKEN','{}',NOW())`, endpointID, storeID)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := EnqueueTestTx(ctx, tx, TestDeliveryInput{StoreID: storeID, EndpointID: endpointID, RequestID: uuid.NewString()})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE "notification_deliveries" SET "availableAt"=NOW()-interval '1 hour',"createdAt"=NOW()-interval '1 hour' WHERE "id"=$1`, first.ID)
	exec(`INSERT INTO "alerts" ("id","storeId","type","severity","detectedAt","updatedAt") VALUES ($1,$2,'WEAPON_DETECTED','CRITICAL',NOW(),NOW())`, alertID, storeID)
	exec(`INSERT INTO "notification_rules" ("id","storeId","name","minimumSeverity","alertTypes","cooldownSeconds","updatedAt") VALUES ($1,$2,'Emergency','CRITICAL',ARRAY['WEAPON_DETECTED']::"AlertType"[],0,NOW())`, ruleID, storeID)
	exec(`INSERT INTO "notification_rule_channels" ("id","storeId","ruleId","endpointId","priority","fallbackDelaySeconds","updatedAt") VALUES ($1,$2,$3,$4,1,0,NOW())`, channelID, storeID, ruleID, endpointID)
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deliveries, err := EnqueueAlertTx(ctx, tx, AlertNotificationInput{StoreID: storeID, AlertID: alertID, AlertType: "WEAPON_DETECTED", Severity: "CRITICAL", DetectedAt: time.Now()})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected one alert job, got %d", len(deliveries))
	}
	worker := NewWorker(db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, WorkerOptions{BatchSize: 1, LeaseDuration: 15 * time.Second})
	claims, err := worker.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].ID != deliveries[0].ID || claims[0].Kind != DeliveryKindAlert {
		t.Fatalf("older TEST displaced ALERT: %+v", claims)
	}
	// No sender is configured or invoked by this test.
}

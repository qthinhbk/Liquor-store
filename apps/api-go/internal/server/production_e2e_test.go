package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/liquor-store/security-api/internal/config"
)

func TestProductionEmergencyNotificationE2E(t *testing.T) {
	if os.Getenv("RUN_PRODUCTION_E2E") != "1" {
		t.Skip("set RUN_PRODUCTION_E2E=1 to create exactly one controlled production alert")
	}
	_ = godotenv.Load("../../.env")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.TrimSpace(cfg.AIIngestToken) == "" {
		t.Fatal("AI_INGEST_TOKEN must match the production Render secret")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("EMERGENCY_API_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "https://liquor-store-api-7tq2.onrender.com"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	storeID, cameraID, err := productionAlertScope(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	storageKey, err := unusedProductionEvidenceKey(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	payload := map[string]any{
		"sourceEventId": "task6-production-e2e-" + uuid.NewString(),
		"correlationId": "task6-controlled-incident-" + uuid.NewString(),
		"storeId":       storeID, "cameraId": cameraID, "type": "WEAPON_DETECTED", "severity": "CRITICAL",
		"subjectPersonCategory": "UNKNOWN", "detectedAt": now,
		"observedStartAt": now.Add(-10 * time.Second), "observedEndAt": now,
		"metadata": map[string]any{"controlledTest": true, "purpose": "task6-production-e2e"},
		"evidence": []map[string]any{{
			"storageKey": storageKey, "mimeType": "video/mp4",
			"durationSeconds": 11, "startsAt": now.Add(-10 * time.Second), "endsAt": now.Add(time.Second),
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/internal/ai/alerts", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+cfg.AIIngestToken)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("call production ingestion endpoint: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("production ingestion returned HTTP %d", response.StatusCode)
	}
	var created aiAlertIngestResponse
	if err := json.Unmarshal(responseBody, &created); err != nil {
		t.Fatal("production ingestion returned invalid JSON")
	}
	if !created.Created || len(created.Deliveries) < 1 {
		t.Fatalf("production ingestion did not create notification routes")
	}

	deadline := time.Now().Add(2 * time.Minute)
	for {
		var sent, active, failed int
		err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE "status"='SENT'),count(*) FILTER (WHERE "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED','WAITING_FALLBACK')),count(*) FILTER (WHERE "status"='FAILED') FROM "notification_deliveries" WHERE "alertId"=$1`, created.AlertID).Scan(&sent, &active, &failed)
		if err != nil {
			t.Fatal(err)
		}
		if sent > 0 && active == 0 {
			break
		}
		if failed > 0 && active == 0 {
			t.Fatal("all production delivery routes reached a terminal failure")
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the production notification worker")
		}
		time.Sleep(2 * time.Second)
	}

	var alertCount, evidenceCount, attemptCount, sentCount int
	err = pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM "alerts" WHERE "id"=$1 AND "sourceEventId" LIKE 'task6-production-e2e-%'),
  (SELECT count(*) FROM "alert_evidence" WHERE "alertId"=$1),
  (SELECT count(*) FROM "notification_attempts" a JOIN "notification_deliveries" d ON d."id"=a."deliveryId" WHERE d."alertId"=$1),
  (SELECT count(*) FROM "notification_deliveries" WHERE "alertId"=$1 AND "status"='SENT')`, created.AlertID).Scan(&alertCount, &evidenceCount, &attemptCount, &sentCount)
	if err != nil {
		t.Fatal(err)
	}
	if alertCount != 1 || evidenceCount != 1 || attemptCount < 1 || sentCount < 1 {
		t.Fatalf("incomplete production E2E audit: alert=%d evidence=%d attempts=%d sent=%d", alertCount, evidenceCount, attemptCount, sentCount)
	}
	t.Log("production E2E passed: one UUID alert, one evidence clip, transactional routes, worker attempt and provider send; identifiers were not printed")
}

func unusedProductionEvidenceKey(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	for _, candidate := range []string{
		"demo-source/alerts/weapon/weapon-review.mp4",
		"demo-source/alerts/weapon/weapon-review-02.mp4",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "alert_evidence" WHERE "storageKey"=$1)`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", errors.New("controlled production evidence fixtures have already been used")
}

func productionAlertScope(ctx context.Context, pool *pgxpool.Pool) (string, string, error) {
	rows, err := pool.Query(ctx, `SELECT s."id",c."id" FROM "stores" s JOIN LATERAL (SELECT "id" FROM "cameras" WHERE "storeId"=s."id" AND "isEnabled" ORDER BY CASE WHEN lower("name") LIKE '%whole store%' THEN 0 ELSE 1 END,"createdAt","id" LIMIT 1) c ON true ORDER BY s."createdAt",s."id" LIMIT 2`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	type scope struct{ storeID, cameraID string }
	items := []scope{}
	for rows.Next() {
		var item scope
		if err := rows.Scan(&item.storeID, &item.cameraID); err != nil {
			return "", "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if len(items) != 1 {
		return "", "", fmt.Errorf("expected exactly one production store with an enabled camera, found %d", len(items))
	}
	return items[0].storeID, items[0].cameraID, nil
}

package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAIAlertIngestionIsAtomicAndIdempotent(t *testing.T) {
	client := newNotifyTestServer(t, false)
	credentials := newNotificationTestCredentials("-ai-ingest")
	userID, storeID := client.registerNotificationUser(t, credentials)
	client.token = client.login(t, credentials.Email, credentials.Password)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tx, err := client.pool.Begin(ctx)
		if err != nil {
			t.Errorf("begin AI ingestion cleanup: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, `DELETE FROM "notification_deliveries" WHERE "storeId"=$1`, storeID); err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM "stores" WHERE "id"=$1`, storeID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM "users" WHERE "id"=$1`, userID)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			t.Errorf("clean AI ingestion data: %v", err)
		}
	})

	var camera cameraResponse
	doJSON(t, client.http, http.MethodPost, client.base+"/api/v1/stores/"+storeID+"/cameras", client.token, map[string]any{
		"name": "Whole store", "location": "Sales floor", "protocol": "RTSP",
		"streamGatewayRef": "ai-ingest/" + uuid.NewString(), "isEnabled": true,
	}, http.StatusCreated, &camera)

	var endpoint notificationEndpointResponse
	doJSON(t, client.http, http.MethodPost, client.base+"/api/v1/stores/"+storeID+"/notification-endpoints", client.token, map[string]any{
		"provider": "TELEGRAM", "label": "AI ingestion test", "destinationRef": "5500123456",
		"credentialRef": "env://TELEGRAM_BOT_TOKEN", "config": map[string]any{"parseMode": "HTML"}, "isEnabled": true,
	}, http.StatusCreated, &endpoint)
	var rule notificationRuleResponse
	doJSON(t, client.http, http.MethodPost, client.base+"/api/v1/stores/"+storeID+"/notification-rules", client.token, map[string]any{
		"name": "Emergency weapon alerts", "minimumSeverity": "CRITICAL", "alertTypes": []string{"WEAPON_DETECTED"},
	}, http.StatusCreated, &rule)
	doJSON(t, client.http, http.MethodPost, client.base+"/api/v1/stores/"+storeID+"/notification-rules/"+rule.ID+"/channels", client.token, map[string]any{
		"endpointId": endpoint.ID, "priority": 1,
	}, http.StatusCreated, &notificationRuleChannelResponse{})

	now := time.Now().UTC().Truncate(time.Millisecond)
	sourceEventID := "ai-e2e-" + uuid.NewString()
	storageKey := "tests/ai-ingest/" + uuid.NewString() + ".mp4"
	payload := map[string]any{
		"sourceEventId": sourceEventID, "correlationId": "incident-" + uuid.NewString(),
		"storeId": storeID, "cameraId": camera.ID, "type": "WEAPON_DETECTED", "severity": "CRITICAL",
		"subjectPersonCategory": "UNKNOWN", "confidence": 0.94, "detectedAt": now,
		"observedStartAt": now.Add(-10 * time.Second), "observedEndAt": now,
		"metadata": map[string]any{"model": "integration-fixture"},
		"evidence": []map[string]any{{
			"storageKey": storageKey, "mimeType": "video/mp4", "durationSeconds": 11,
			"startsAt": now.Add(-10 * time.Second), "endsAt": now.Add(time.Second),
		}},
	}
	path := client.base + "/api/v1/internal/ai/alerts"
	var created aiAlertIngestResponse
	doJSON(t, client.http, http.MethodPost, path, testAIIngestToken, payload, http.StatusCreated, &created)
	if !created.Created || created.AlertID == "" || created.SourceEventID != sourceEventID || len(created.Deliveries) != 1 {
		t.Fatalf("unexpected ingestion response: %+v", created)
	}
	if _, err := uuid.Parse(created.AlertID); err != nil {
		t.Fatalf("server did not create a UUID alert ID: %q", created.AlertID)
	}
	if created.Deliveries[0].Status != "PENDING" || created.Deliveries[0].Provider != "TELEGRAM" {
		t.Fatalf("unexpected delivery route: %+v", created.Deliveries[0])
	}

	var repeated aiAlertIngestResponse
	doJSON(t, client.http, http.MethodPost, path, testAIIngestToken, payload, http.StatusOK, &repeated)
	if repeated.Created || repeated.AlertID != created.AlertID || len(repeated.Deliveries) != 1 || repeated.Deliveries[0].ID != created.Deliveries[0].ID {
		t.Fatalf("source event retry was not idempotent: first=%+v repeat=%+v", created, repeated)
	}

	var alertCount, evidenceCount, deliveryCount int
	if err := client.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM "alerts" WHERE "sourceEventId"=$1),(SELECT count(*) FROM "alert_evidence" WHERE "alertId"=$2),(SELECT count(*) FROM "notification_deliveries" WHERE "alertId"=$2)`, sourceEventID, created.AlertID).Scan(&alertCount, &evidenceCount, &deliveryCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 1 || evidenceCount != 1 || deliveryCount != 1 {
		t.Fatalf("ingestion was not atomic/idempotent: alerts=%d evidence=%d deliveries=%d", alertCount, evidenceCount, deliveryCount)
	}

	var detail alertDetailResponse
	doJSON(t, client.http, http.MethodGet, client.base+"/api/v1/stores/"+storeID+"/alerts/"+created.AlertID, client.token, nil, http.StatusOK, &detail)
	if !detail.HasVideoEvidence || len(detail.Evidence) != 1 || detail.Evidence[0].ID == "" || detail.Evidence[0].StorageKey != "" {
		t.Fatalf("owner alert detail is missing video evidence: %+v", detail)
	}

	doJSON(t, client.http, http.MethodPost, path, "wrong-ai-token", payload, http.StatusUnauthorized, &map[string]any{})
	conflict := cloneMap(payload)
	conflict["sourceEventId"] = "ai-e2e-" + uuid.NewString()
	doJSON(t, client.http, http.MethodPost, path, testAIIngestToken, conflict, http.StatusConflict, &map[string]any{})
	if err := client.pool.QueryRow(context.Background(), `SELECT count(*) FROM "alerts" WHERE "storeId"=$1`, storeID).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 1 {
		t.Fatalf("evidence conflict left a partial alert behind: %d alerts", alertCount)
	}
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

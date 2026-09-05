package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liquor-store/security-api/internal/config"
	"github.com/liquor-store/security-api/internal/migrations"
	"github.com/liquor-store/security-api/internal/notifications"
)

func securityTestDatabase(t *testing.T) string {
	t.Helper()
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("requires RUN_INTEGRATION_TESTS=1 and an explicit disposable database")
	}
	dsn := strings.TrimSpace(os.Getenv("NOTIFICATION_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Fatal("NOTIFICATION_TEST_DATABASE_URL is required; runtime DATABASE_URL is never used")
	}
	return dsn
}

func TestSecurityMigrations(t *testing.T) {
	conn, err := pgx.Connect(context.Background(), securityTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err = migrations.Apply(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	result, err := migrations.Apply(context.Background(), conn)
	if err != nil || result != "Database is already up to date." {
		t.Fatalf("migration re-run: %q %v", result, err)
	}
}

func TestSecurityRegressionPostgreSQL(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), securityTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := context.Background()
	ownerID, operatorID, storeID, otherStore := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	cfg := config.Config{JWTAccessSecret: "security-regression-test-key-not-production", JWTAccessTTL: time.Minute * 15, JWTIssuer: "security-tests", JWTAudience: "security-tests", NotificationCredentialBindings: []notifications.CredentialBinding{{StoreID: storeID, Provider: notifications.ProviderTelegram, CredentialRef: "env://TELEGRAM_BOT_TOKEN"}}}
	api := New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	exec := func(t *testing.T, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Only this test's data is removed. Audit snapshots intentionally survive
	// until the explicitly disposable database is dropped by the test runner.
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM "notification_deliveries" WHERE "storeId"=ANY($1::uuid[])`, []string{storeID, otherStore})
		_, _ = pool.Exec(ctx, `DELETE FROM "stores" WHERE "id"=ANY($1::uuid[]) OR "code"=$2`, []string{storeID, otherStore}, "extra-"+storeID[:24])
		_, _ = pool.Exec(ctx, `DELETE FROM "users" WHERE "id"=ANY($1::uuid[])`, []string{ownerID, operatorID})
	}()
	for _, id := range []string{ownerID, operatorID} {
		exec(t, `INSERT INTO "users" ("id","email","passwordHash","displayName","updatedAt") VALUES ($1,$2,'unused-test-hash','Security test',NOW())`, id, id+"@example.test")
	}
	for _, id := range []string{storeID, otherStore} {
		exec(t, `INSERT INTO "stores" ("id","name","code","updatedAt") VALUES ($1,'Security test',$2,NOW())`, id, id)
	}
	for _, member := range []struct{ id, role string }{{ownerID, "OWNER"}, {operatorID, "OPERATOR"}} {
		exec(t, `INSERT INTO "store_memberships" ("id","userId","storeId","role","updatedAt") VALUES ($1,$2,$3,$4,NOW())`, uuid.NewString(), member.id, storeID, member.role)
	}
	ownerToken, _ := api.signAccessToken(authUser{ID: ownerID})
	operatorToken, _ := api.signAccessToken(authUser{ID: operatorID})
	request := func(method, path, token string, payload any) (int, []byte) {
		raw, _ := json.Marshal(payload)
		req, _ := http.NewRequest(method, server.URL+"/api/v1"+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := server.Client().Do(req)
		if err != nil {
			return 0, []byte(err.Error())
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, body
	}
	expect := func(t *testing.T, method, path, token string, payload any, want int) []byte {
		t.Helper()
		code, body := request(method, path, token, payload)
		if code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, body)
		}
		return body
	}

	t.Run("store creation and account suspension", func(t *testing.T) {
		body := map[string]any{"name": "Extra store", "code": "extra-" + storeID[:24]}
		expect(t, "POST", "/stores", operatorToken, body, 403)
		exec(t, `UPDATE "users" SET "status"='SUSPENDED' WHERE "id"=$1`, ownerID)
		for _, route := range []struct {
			method, path string
			body         any
		}{{"GET", "/stores", nil}, {"POST", "/stores", body}, {"GET", "/users/me", nil}, {"PATCH", "/users/me", map[string]any{"displayName": "changed"}}} {
			expect(t, route.method, route.path, ownerToken, route.body, 401)
		}
		exec(t, `UPDATE "users" SET "status"='ACTIVE' WHERE "id"=$1`, ownerID)
		expect(t, "POST", "/stores", ownerToken, body, 201)
	})

	alertID := uuid.NewString()
	exec(t, `INSERT INTO "alerts" ("id","storeId","type","severity","status","detectedAt","acknowledgedById","acknowledgedAt","resolutionNote","updatedAt") VALUES ($1,$2,'SUSPICIOUS_PRODUCT_CONCEALMENT','HIGH','RESOLVED',NOW(),$3,NOW(),'original manager decision',NOW())`, alertID, storeID, ownerID)
	t.Run("terminal decision permission and immutable history", func(t *testing.T) {
		path := "/stores/" + storeID + "/alerts/" + alertID + "/dismiss"
		expect(t, "POST", path, operatorToken, map[string]any{}, 403)
		var status, actor, note string
		if err := pool.QueryRow(ctx, `SELECT "status","acknowledgedById","resolutionNote" FROM "alerts" WHERE "id"=$1`, alertID).Scan(&status, &actor, &note); err != nil {
			t.Fatal(err)
		}
		if status != "RESOLVED" || actor != ownerID || note != "original manager decision" {
			t.Fatal("blocked decision changed stored data")
		}
		var wg sync.WaitGroup
		codes := make(chan int, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); code, _ := request("POST", path, ownerToken, map[string]any{}); codes <- code }()
		}
		wg.Wait()
		close(codes)
		counts := map[int]int{}
		for code := range codes {
			counts[code]++
		}
		if counts[201] != 1 || counts[409] != 1 {
			t.Fatalf("concurrent decisions: %v", counts)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM "alert_status_history" WHERE "alertId"=$1 AND "previousStatus"='RESOLVED' AND "newStatus"='DISMISSED' AND "previousActorId"=$2 AND "previousNote"='original manager decision' AND "note"='original manager decision'`, alertID, ownerID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("audit history: %d %v", n, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE "alert_status_history" SET "note"='tampered' WHERE "alertId"=$1`, alertID); err == nil {
			t.Fatal("audit UPDATE allowed")
		}
		if _, err := pool.Exec(ctx, `DELETE FROM "alert_status_history" WHERE "alertId"=$1`, alertID); err == nil {
			t.Fatal("audit DELETE allowed")
		}
		if _, err := pool.Exec(ctx, `TRUNCATE "alert_status_history"`); err == nil {
			t.Fatal("audit TRUNCATE allowed")
		}
	})

	endpoints := "/stores/" + storeID + "/notification-endpoints"
	endpointPayload := map[string]any{"provider": "TELEGRAM", "label": "Security test", "destinationRef": "test-destination", "credentialRef": "env://JWT_ACCESS_SECRET"}
	t.Run("API credential scope", func(t *testing.T) {
		expect(t, "POST", endpoints, ownerToken, endpointPayload, 403)
		endpointPayload["credentialRef"] = "env://TELEGRAM_BOT_TOKEN"
		endpointPayload["providerAccountRef"] = "unassigned-account"
		expect(t, "POST", endpoints, ownerToken, endpointPayload, 403)
		delete(endpointPayload, "providerAccountRef")
		// Even a store owner cannot borrow the first store's credential assignment.
		exec(t, `INSERT INTO "store_memberships" ("id","userId","storeId","role","updatedAt") VALUES ($1,$2,$3,'OWNER',NOW())`, uuid.NewString(), ownerID, otherStore)
		expect(t, "POST", "/stores/"+otherStore+"/notification-endpoints", ownerToken, endpointPayload, 403)
	})
	var endpoint notificationEndpointResponse
	if err := json.Unmarshal(expect(t, "POST", endpoints, ownerToken, endpointPayload, 201), &endpoint); err != nil {
		t.Fatal(err)
	}
	if err := notifications.CheckCredentialBindings(ctx, pool, cfg.NotificationCredentialBindings); err != nil {
		t.Fatalf("valid binding preflight failed: %v", err)
	}
	if err := notifications.CheckCredentialBindings(ctx, pool, nil); err == nil {
		t.Fatal("worker startup accepted an unbound active endpoint")
	}
	t.Run("test quota is atomic and idempotent", func(t *testing.T) {
		path := endpoints + "/" + endpoint.ID + "/test"
		var wg sync.WaitGroup
		type outcome struct {
			code int
			id   string
		}
		results := make(chan outcome, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := uuid.NewString()
				code, _ := request("POST", path, ownerToken, map[string]any{"requestId": id})
				results <- outcome{code, id}
			}()
		}
		wg.Wait()
		close(results)
		accepted, rejected := 0, 0
		id := ""
		for result := range results {
			switch result.code {
			case 202:
				accepted++
				id = result.id
			case 429:
				rejected++
			default:
				t.Errorf("unexpected quota response %d", result.code)
			}
		}
		if accepted != 1 || rejected != 11 {
			t.Fatalf("admission accepted=%d rejected=%d", accepted, rejected)
		}
		expect(t, "POST", path, ownerToken, map[string]any{"requestId": id}, 202)
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM "notification_deliveries" WHERE "storeId"=$1 AND "requestedById"=$2`, storeID, ownerID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("queue count %d %v", n, err)
		}
		// Changing a stored endpoint cannot evade worker policy; API rejects a test
		// immediately, and scoped sender tests cover the final outbound check.
		exec(t, `UPDATE "notification_endpoints" SET "credentialRef"='env://JWT_ACCESS_SECRET' WHERE "id"=$1`, endpoint.ID)
		expect(t, "POST", path, ownerToken, map[string]any{"requestId": uuid.NewString()}, 403)
		expect(t, "PATCH", endpoints+"/"+endpoint.ID, ownerToken, map[string]any{"isEnabled": false}, 200)
	})
	t.Log(fmt.Sprintf("Security regressions verified for isolated store %s", storeID))
}

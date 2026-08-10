package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/liquor-store/security-api/internal/config"
)

func TestNeonAPISmoke(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_INTEGRATION_TESTS=1 to run the real database smoke test")
	}

	_ = godotenv.Load("../../.env")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.RegisterEnabled = true
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(pool.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	testServer := httptest.NewServer(New(cfg, pool, logger).Handler())
	t.Cleanup(testServer.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	email := "go-smoke-" + suffix + "@example.test"
	password := "Smoke-only-" + suffix
	var registered sessionResponse
	doJSON(t, client, http.MethodPost, testServer.URL+"/api/v1/auth/register", "", map[string]any{
		"email": email, "password": password, "displayName": "Go Smoke Owner",
		"storeName": "Go Smoke Store", "storeCode": "go-smoke-" + suffix,
	}, http.StatusCreated, &registered)
	if len(registered.User.Stores) != 1 {
		t.Fatalf("expected one registered store, got %d", len(registered.User.Stores))
	}
	userID, storeID := registered.User.ID, registered.User.Stores[0].StoreID
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tx, cleanupErr := pool.Begin(ctx)
		if cleanupErr != nil {
			t.Errorf("begin cleanup: %v", cleanupErr)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, cleanupErr = tx.Exec(ctx, `DELETE FROM "stores" WHERE "id"=$1`, storeID); cleanupErr == nil {
			_, cleanupErr = tx.Exec(ctx, `DELETE FROM "users" WHERE "id"=$1`, userID)
		}
		if cleanupErr == nil {
			cleanupErr = tx.Commit(ctx)
		}
		if cleanupErr != nil {
			t.Errorf("clean smoke data: %v", cleanupErr)
		}
	})

	var loggedIn sessionResponse
	doJSON(t, client, http.MethodPost, testServer.URL+"/api/v1/auth/login", "", map[string]any{
		"email": email, "password": password,
	}, http.StatusCreated, &loggedIn)
	token := loggedIn.AccessToken
	refreshURL, err := url.Parse(testServer.URL + "/api/v1/auth/refresh")
	if err != nil {
		t.Fatal(err)
	}
	var originalRefreshCookie *http.Cookie
	for _, cookie := range client.Jar.Cookies(refreshURL) {
		if cookie.Name == "refresh_token" {
			copy := *cookie
			originalRefreshCookie = &copy
		}
	}
	if originalRefreshCookie == nil {
		t.Fatal("login did not issue a refresh cookie")
	}
	doJSON(t, client, http.MethodGet, testServer.URL+"/api/v1/auth/me", token, nil, http.StatusOK, &currentUser{})
	doJSON(t, client, http.MethodGet, testServer.URL+"/api/v1/stores", token, nil, http.StatusOK, &[]storeResponse{})

	var camera cameraResponse
	doJSON(t, client, http.MethodPost, testServer.URL+"/api/v1/stores/"+storeID+"/cameras", token, map[string]any{
		"name": "Smoke Camera", "location": "Test Area", "protocol": "RTSP",
		"streamGatewayRef": "smoke/camera/" + suffix, "isEnabled": true,
	}, http.StatusCreated, &camera)
	doJSON(t, client, http.MethodPatch, testServer.URL+"/api/v1/stores/"+storeID+"/cameras/"+camera.ID, token, map[string]any{
		"status": "ONLINE", "name": "Smoke Camera Updated",
	}, http.StatusOK, &cameraResponse{})

	var zone zoneResponse
	doJSON(t, client, http.MethodPost, testServer.URL+"/api/v1/stores/"+storeID+"/cameras/"+camera.ID+"/zones", token, map[string]any{
		"name": "Cashier Boundary", "kind": "CASHIER", "expectedPersonCategory": "EMPLOYEE",
		"polygon": [][]float64{{0.1, 0.1}, {0.9, 0.1}, {0.9, 0.9}, {0.1, 0.9}},
	}, http.StatusCreated, &zone)
	doJSON(t, client, http.MethodPatch, testServer.URL+"/api/v1/stores/"+storeID+"/cameras/"+camera.ID+"/zones/"+zone.ID, token, map[string]any{
		"dwellThresholdSeconds": 30,
	}, http.StatusOK, &zoneResponse{})
	doJSON(t, client, http.MethodGet, testServer.URL+"/api/v1/stores/"+storeID+"/cameras", token, nil, http.StatusOK, &[]cameraResponse{})
	doJSON(t, client, http.MethodGet, testServer.URL+"/api/v1/stores/"+storeID+"/cameras/"+camera.ID+"/zones", token, nil, http.StatusOK, &[]zoneResponse{})
	doJSON(t, client, http.MethodGet, testServer.URL+"/api/v1/stores/"+storeID+"/alerts", token, nil, http.StatusOK, &map[string]any{})

	var refreshed sessionResponse
	doJSON(t, client, http.MethodPost, testServer.URL+"/api/v1/auth/refresh", "", nil, http.StatusCreated, &refreshed)
	token = refreshed.AccessToken
	replayRequest, err := http.NewRequest(http.MethodPost, testServer.URL+"/api/v1/auth/refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	replayRequest.AddCookie(originalRefreshCookie)
	replayResponse, err := (&http.Client{Timeout: 30 * time.Second}).Do(replayRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = replayResponse.Body.Close()
	if replayResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed refresh token: expected 401, got %d", replayResponse.StatusCode)
	}
	doJSON(t, client, http.MethodDelete, testServer.URL+"/api/v1/stores/"+storeID+"/cameras/"+camera.ID+"/zones/"+zone.ID, token, nil, http.StatusOK, &map[string]bool{})
	doJSON(t, client, http.MethodDelete, testServer.URL+"/api/v1/stores/"+storeID+"/cameras/"+camera.ID, token, nil, http.StatusOK, &map[string]bool{})
	doJSON(t, client, http.MethodPost, testServer.URL+"/api/v1/auth/logout", "", nil, http.StatusCreated, &map[string]bool{})
}

func doJSON(t *testing.T, client *http.Client, method, url, token string, input any, expected int, output any) {
	t.Helper()
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != expected {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, url, expected, response.StatusCode, payload)
	}
	if output != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
}

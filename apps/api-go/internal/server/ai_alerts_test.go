package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/liquor-store/security-api/internal/config"
)

const testAIIngestToken = "test-ai-ingest-token-that-is-at-least-32-characters"

func validAIAlertFixture() aiAlertInput {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	confidence := 0.91
	return aiAlertInput{
		SourceEventID: "edge-event-001", CorrelationID: stringPointer("incident-001"),
		StoreID: uuid.NewString(), CameraID: uuid.NewString(), Type: "weapon_detected", Severity: "critical",
		SubjectPersonCategory: "unknown", Confidence: &confidence, DetectedAt: now,
		ObservedStartAt: timePointer(now.Add(-10 * time.Second)), ObservedEndAt: timePointer(now),
		Metadata: []byte(`{"model":"edge-security-v1"}`),
		Evidence: []aiEvidenceInput{{
			StorageKey: "demo-source/alerts/weapon/weapon-review.mp4", MimeType: "video/mp4",
			DurationSeconds: 11, StartsAt: now.Add(-10 * time.Second), EndsAt: now.Add(time.Second),
		}},
	}
}

func TestNormalizeAndValidateAIAlert(t *testing.T) {
	input := validAIAlertFixture()
	if message := normalizeAndValidateAIAlert(&input); message != "" {
		t.Fatalf("valid input rejected: %s", message)
	}
	if input.Type != "WEAPON_DETECTED" || input.Severity != "CRITICAL" || input.SubjectPersonCategory != "UNKNOWN" {
		t.Fatalf("enum values were not normalized: %+v", input)
	}

	tests := []struct {
		name   string
		mutate func(*aiAlertInput)
		want   string
	}{
		{"source event required", func(value *aiAlertInput) { value.SourceEventID = "" }, "sourceEventId"},
		{"store uuid", func(value *aiAlertInput) { value.StoreID = "not-a-uuid" }, "storeId"},
		{"camera uuid", func(value *aiAlertInput) { value.CameraID = "not-a-uuid" }, "cameraId"},
		{"alert type", func(value *aiAlertInput) { value.Type = "FACE_IDENTIFIED" }, "type"},
		{"confidence", func(value *aiAlertInput) { invalid := 1.1; value.Confidence = &invalid }, "confidence"},
		{"evidence required", func(value *aiAlertInput) { value.Evidence = nil }, "evidence"},
		{"relative storage key", func(value *aiAlertInput) { value.Evidence[0].StorageKey = "../secret.mp4" }, "storageKey"},
		{"video evidence", func(value *aiAlertInput) { value.Evidence[0].MimeType = "image/jpeg" }, "mimeType"},
		{"evidence window", func(value *aiAlertInput) { value.Evidence[0].EndsAt = value.Evidence[0].StartsAt }, "startsAt"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := validAIAlertFixture()
			testCase.mutate(&value)
			if message := normalizeAndValidateAIAlert(&value); !strings.Contains(message, testCase.want) {
				t.Fatalf("expected validation mentioning %q, got %q", testCase.want, message)
			}
		})
	}
}

func TestAIIngestAuthentication(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(config.Config{AIIngestToken: testAIIngestToken}, nil, logger)
	handler := server.aiIngestEndpoint(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, testCase := range []struct {
		name   string
		token  string
		status int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"invalid", "wrong-token", http.StatusUnauthorized},
		{"valid", testAIIngestToken, http.StatusNoContent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/internal/ai/alerts", bytes.NewReader([]byte(`{}`)))
			if testCase.token != "" {
				request.Header.Set("Authorization", "Bearer "+testCase.token)
			}
			handler.ServeHTTP(recorder, request)
			if recorder.Code != testCase.status {
				t.Fatalf("expected %d, got %d", testCase.status, recorder.Code)
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("AI ingestion responses must not be cached")
			}
		})
	}
}

func TestAIIngestRouteDisabledWithoutToken(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/internal/ai/alerts", nil)
	newTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected disabled route to return 404, got %d", recorder.Code)
	}
}

func stringPointer(value string) *string     { return &value }
func timePointer(value time.Time) *time.Time { return &value }

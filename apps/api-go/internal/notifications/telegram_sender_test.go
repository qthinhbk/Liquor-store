package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fakeTelegramToken = "123456789:TEST_FAKE_TOKEN_NOT_REAL"
	fakeTelegramChat  = "550001"
)

type telegramTestHarness struct {
	sender *TelegramSender
	hits   *atomic.Int32
}

func newTelegramTestSender(t *testing.T, handler http.HandlerFunc) telegramTestHarness {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST", fakeTelegramToken)
	var hits atomic.Int32
	wrapped := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		handler(writer, request)
	})
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)
	resolver := NewEnvCredentialResolver()
	sender := NewTelegramSender(resolver, TelegramSenderOptions{BaseURL: server.URL})
	return telegramTestHarness{sender: sender, hits: &hits}
}

func alertSendRequest(config json.RawMessage) SendRequest {
	payload := BuildAlertPayload(AlertNotificationInput{
		StoreID:       "store-1",
		StoreName:     "Liquor Store",
		StoreTimezone: "America/Chicago",
		AlertID:       "alert-1",
		AlertType:     "WEAPON_DETECTED",
		Severity:      "CRITICAL",
		DetectedAt:    time.Date(2026, 8, 24, 9, 42, 0, 0, time.UTC),
		CameraID:      "camera-1",
		CameraName:    "Whole store",
		StorageKey:    "evidence/alert-1/clip.mp4",
	})
	return SendRequest{
		DeliveryID:      uuidForTest(),
		Provider:        ProviderTelegram,
		DestinationRef:  fakeTelegramChat,
		CredentialRef:   "env://TELEGRAM_BOT_TOKEN_UNIT_TEST",
		Config:          config,
		TemplateVersion: TelegramTemplateVersion,
		Payload:         payload,
	}
}

func testSendRequest() SendRequest {
	return SendRequest{
		DeliveryID:      uuidForTest(),
		Provider:        ProviderTelegram,
		DestinationRef:  fakeTelegramChat,
		CredentialRef:   "env://TELEGRAM_BOT_TOKEN_UNIT_TEST",
		TemplateVersion: TelegramTemplateVersion,
		Payload:         BuildTestPayload(ProviderTelegram),
	}
}

func assertTransient(t *testing.T, err error, expectedCode string) *TransientSendError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var transient *TransientSendError
	if !errors.As(err, &transient) {
		t.Fatalf("expected transient error, got %T: %v", err, err)
	}
	if expectedCode != "" && transient.Code != expectedCode {
		t.Fatalf("expected code %s, got %s", expectedCode, transient.Code)
	}
	assertNoTokenLeak(t, err.Error())
	return transient
}

func assertPermanent(t *testing.T, err error, expectedCode string) *PermanentSendError {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var permanent *PermanentSendError
	if !errors.As(err, &permanent) {
		t.Fatalf("expected permanent error, got %T: %v", err, err)
	}
	if expectedCode != "" && permanent.Code != expectedCode {
		t.Fatalf("expected code %s, got %s", expectedCode, permanent.Code)
	}
	assertNoTokenLeak(t, err.Error())
	return permanent
}

func assertNoTokenLeak(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, fakeTelegramToken) || strings.Contains(text, fakeTelegramChat) {
		t.Fatalf("secret or destination leaked into error output: %s", text)
	}
}

func TestTelegramSenderProvider(t *testing.T) {
	sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{})
	if sender.Provider() != ProviderTelegram {
		t.Fatal("provider must be TELEGRAM")
	}
}

func TestTelegramAlertMessageRendersOwnerFacingFields(t *testing.T) {
	var capturedBody string
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 8192)
		read, _ := request.Body.Read(buffer)
		capturedBody = string(buffer[:read])
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":42}}`)
	})
	result, err := harness.sender.Send(context.Background(), alertSendRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, expected := range []string{"Potential emergency", "Store: Liquor Store", "Camera: Whole store", "Detected:", "emergency procedure"} {
		if !strings.Contains(capturedBody, expected) {
			t.Fatalf("message missing %q: %s", expected, capturedBody)
		}
	}
	for _, forbidden := range []string{"storageKey", "evidence/alert-1", "rtsp://", "credentialRef", fakeTelegramToken, "alert-1"} {
		if strings.Contains(capturedBody, forbidden) {
			t.Fatalf("message leaks %q: %s", forbidden, capturedBody)
		}
	}
	if result.ProviderMessageID != "42" {
		t.Fatalf("unexpected message id %q", result.ProviderMessageID)
	}
}

func TestTelegramAlertMessageIncludesEscapedEphemeralReviewURL(t *testing.T) {
	var capturedBody string
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 8192)
		read, _ := request.Body.Read(buffer)
		capturedBody = string(buffer[:read])
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":43}}`)
	})
	request := alertSendRequest(json.RawMessage(`{"parseMode":"HTML"}`))
	request.Payload.ReviewURL = `https://api.example.test/api/v1/notification-review/opaque-token?a=1&b=2`
	if _, err := harness.sender.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(capturedBody), &decoded); err != nil {
		t.Fatal(err)
	}
	message, _ := decoded["text"].(string)
	if !strings.Contains(message, `href="https://api.example.test/api/v1/notification-review/opaque-token?a=1&amp;b=2"`) {
		t.Fatalf("review URL was not safely rendered: %s", message)
	}
	encodedPayload, err := json.Marshal(request.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedPayload), "opaque-token") {
		t.Fatalf("ephemeral review URL must never serialize into delivery payload: %s", encodedPayload)
	}
}

func TestTelegramTestMessageStatesItIsATest(t *testing.T) {
	var capturedBody string
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 8192)
		read, _ := request.Body.Read(buffer)
		capturedBody = string(buffer[:read])
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":7}}`)
	})
	if _, err := harness.sender.Send(context.Background(), testSendRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lower := strings.ToLower(capturedBody)
	if !strings.Contains(lower, "test notification") || !strings.Contains(lower, "no real alert") {
		t.Fatalf("test message must state it is a test without a real alert: %s", capturedBody)
	}
	if strings.Contains(capturedBody, "Potential emergency") {
		t.Fatal("test message must not use the emergency header")
	}
}

func TestTelegramHTMLParseModeEscapesDynamicValues(t *testing.T) {
	malicious := `<b>&"x`
	var capturedBody string
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		buffer := make([]byte, 8192)
		read, _ := request.Body.Read(buffer)
		capturedBody = string(buffer[:read])
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":9}}`)
	})
	request := alertSendRequest(json.RawMessage(`{"parseMode":"HTML"}`))
	request.Payload.StoreName = malicious
	if _, err := harness.sender.Send(context.Background(), request); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedBody, `"parse_mode":"HTML"`) {
		t.Fatalf("parse_mode HTML missing: %s", capturedBody)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(capturedBody), &decoded); err != nil {
		t.Fatal(err)
	}
	text, _ := decoded["text"].(string)
	if strings.Contains(text, "<b>") {
		t.Fatalf("dynamic value was not HTML-escaped: %s", text)
	}
	if !strings.Contains(text, "&lt;b&gt;&amp;&#34;x") {
		t.Fatalf("escaped store name missing: %s", text)
	}
}

func TestTelegramSendsPostJSONToCorrectChat(t *testing.T) {
	var method, contentType string
	var decoded map[string]any
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		method = request.Method
		contentType = request.Header.Get("Content-Type")
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		decoded = body
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":11}}`)
	})
	if _, err := harness.sender.Send(context.Background(), testSendRequest()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if method != http.MethodPost || !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("expected POST JSON, got %s %s", method, contentType)
	}
	if decoded["chat_id"] != fakeTelegramChat {
		t.Fatalf("chat_id = %v, want %s", decoded["chat_id"], fakeTelegramChat)
	}
	if decoded["disable_web_page_preview"] != true {
		t.Fatal("disable_web_page_preview must be true")
	}
	if path := decoded["text"]; path == nil || path == "" {
		t.Fatal("text must not be empty")
	}
}

func TestTelegramSuccessReturnsMessageIDAndSingleRequest(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":123}}`)
	})
	result, err := harness.sender.Send(context.Background(), testSendRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProviderMessageID != "123" {
		t.Fatalf("message id = %q, want 123", result.ProviderMessageID)
	}
	if harness.hits.Load() != 1 {
		t.Fatalf("exactly one provider request allowed, got %d", harness.hits.Load())
	}
	status, ok := result.ResponseMetadata["httpStatus"].(int)
	if !ok || status != http.StatusOK {
		t.Fatalf("metadata must contain non-sensitive httpStatus, got %+v", result.ResponseMetadata)
	}
	if strings.Contains(mustMarshalTelegram(t, result.ResponseMetadata), fakeTelegramToken) {
		t.Fatal("metadata leaked token")
	}
}

func TestTelegramSuccessWithoutMessageIDIsTransient(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writeTelegramJSON(writer, `{"ok":true,"result":{}}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeInvalidResponse)
}

func TestTelegramHTTP429ReadsRetryAfter(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		writeTelegramJSON(writer, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":10}}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	transient := assertTransient(t, err, TelegramCodeRateLimited)
	if transient.RetryAfter != 10*time.Second {
		t.Fatalf("retry after = %v, want 10s", transient.RetryAfter)
	}
	if harness.hits.Load() != 1 {
		t.Fatal("sender must not retry internally")
	}
}

func TestTelegram429WithUnreasonableRetryAfterIsIgnored(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		writeTelegramJSON(writer, `{"ok":false,"error_code":429,"parameters":{"retry_after":999999}}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	transient := assertTransient(t, err, TelegramCodeRateLimited)
	if transient.RetryAfter != 0 {
		t.Fatalf("unreasonable retry_after must be dropped, got %v", transient.RetryAfter)
	}
}

func TestTelegramOKFalseWithErrorCode429IsTransient(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writeTelegramJSON(writer, `{"ok":false,"error_code":429,"description":"retry later","parameters":{"retry_after":5}}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	transient := assertTransient(t, err, TelegramCodeRateLimited)
	if transient.RetryAfter != 5*time.Second {
		t.Fatalf("retry after = %v, want 5s", transient.RetryAfter)
	}
}

func TestTelegramHTTP500IsTransient(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		writeTelegramJSON(writer, `{"ok":false,"error_code":500,"description":"internal"}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeProviderUnavailable)
}

func TestTelegramNetworkFailureIsTransient(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST", fakeTelegramToken)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{BaseURL: server.URL})
	_, err := sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeNetworkError)
}

func TestTelegramContextCancellationIsTransient(t *testing.T) {
	hits := atomic.Int32{}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		<-release
	}))
	t.Cleanup(func() { close(release); server.Close() })
	sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{BaseURL: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := sender.Send(ctx, testSendRequest())
	assertTransient(t, err, TelegramCodeNetworkError)
	if hits.Load() != 0 {
		t.Fatal("cancelled context must not reach the provider")
	}
}

func TestTelegramClientTimeoutIsTransient(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST", fakeTelegramToken)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); server.Close() })
	sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{
		BaseURL:    server.URL,
		HTTPClient: &http.Client{Timeout: 50 * time.Millisecond},
	})
	_, err := sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeNetworkError)
}

func TestTelegram400IsPermanent(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		writeTelegramJSON(writer, `{"ok":false,"error_code":400,"description":"chat not found"}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertPermanent(t, err, TelegramCodeInvalidDestination)
}

func TestTelegram401IsPermanent(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		writeTelegramJSON(writer, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertPermanent(t, err, TelegramCodeUnauthorized)
}

func TestTelegram403IsPermanent(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		writeTelegramJSON(writer, `{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertPermanent(t, err, TelegramCodeForbidden)
}

func TestTelegram404IsPermanent(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		writeTelegramJSON(writer, `{"ok":false,"error_code":404,"description":"Not Found"}`)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertPermanent(t, err, TelegramCodeDestinationNotFound)
}

func TestTelegramEmptyDestinationRejectedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	request := testSendRequest()
	request.DestinationRef = "   "
	_, err := harness.sender.Send(context.Background(), request)
	assertPermanent(t, err, TelegramCodeInvalidDestination)
	if harness.hits.Load() != 0 {
		t.Fatal("validation failure must not reach the provider")
	}
}

func TestTelegramMissingEnvironmentCredentialRejectedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	request := testSendRequest()
	request.CredentialRef = "env://TELEGRAM_BOT_TOKEN_DEFINITELY_UNSET_12345"
	_, err := harness.sender.Send(context.Background(), request)
	assertPermanent(t, err, TelegramCodeMissingCredential)
	if harness.hits.Load() != 0 {
		t.Fatal("credential failure must not reach the provider")
	}
}

func TestTelegramRawTokenCredentialRefRejectedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	request := testSendRequest()
	request.CredentialRef = rawTelegramTokenForTest()
	_, err := harness.sender.Send(context.Background(), request)
	assertPermanent(t, err, TelegramCodeUnsupportedCredRef)
	if harness.hits.Load() != 0 {
		t.Fatal("raw token credentialRef must fail closed before HTTP")
	}
	assertNoTokenLeak(t, fmt.Sprintf("%v", err))
}

func TestTelegramRenderSecretSchemeFailsClosedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	request := testSendRequest()
	request.CredentialRef = "render-secret://telegram/main-bot"
	_, err := harness.sender.Send(context.Background(), request)
	assertPermanent(t, err, TelegramCodeUnsupportedCredRef)
	if harness.hits.Load() != 0 {
		t.Fatal("unsupported scheme must fail closed before HTTP")
	}
}

func TestTelegramMalformedJSONIsTransient(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte(`<html>gateway error</html>`))
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeInvalidResponse)
}

func TestTelegramOversizedResponseIsTransient(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Write([]byte(strings.Repeat("x", maxTelegramResponseBytes+2048)))
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeInvalidResponse)
}

func TestTelegramRedirectIsBlockedAndNeverFollowed(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST", fakeTelegramToken)
			secondHits := atomic.Int32{}
			secondServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				secondHits.Add(1)
				writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":999}}`)
			}))
			t.Cleanup(secondServer.Close)
			firstHits := atomic.Int32{}
			firstServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				firstHits.Add(1)
				http.Redirect(writer, request, secondServer.URL+"/steal", status)
			}))
			t.Cleanup(firstServer.Close)
			sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{BaseURL: firstServer.URL})
			result, err := sender.Send(context.Background(), testSendRequest())
			if err == nil {
				t.Fatal("redirect response must not be reported as success")
			}
			assertTransient(t, err, TelegramCodeInvalidResponse)
			assertNoTokenLeak(t, err.Error())
			if result.ProviderMessageID != "" {
				t.Fatal("redirect must not produce a message id")
			}
			if firstHits.Load() != 1 {
				t.Fatalf("first server must receive exactly one request, got %d", firstHits.Load())
			}
			if secondHits.Load() != 0 {
				t.Fatal("redirect target must never be contacted")
			}
		})
	}
}

func TestTelegramInjectedZeroTimeoutClientIsStillBounded(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST", fakeTelegramToken)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); server.Close() })
	sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{
		BaseURL:        server.URL,
		HTTPClient:     &http.Client{Timeout: 0},
		RequestTimeout: 60 * time.Millisecond,
	})
	started := time.Now()
	_, err := sender.Send(context.Background(), testSendRequest())
	assertTransient(t, err, TelegramCodeNetworkError)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("send waited %v; per-request timeout was not enforced", elapsed)
	}
}

func TestTelegramNilResolverFailsClosedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	harness.sender.resolver = nil
	request := testSendRequest()
	request.CredentialRef = "env://TELEGRAM_BOT_TOKEN_UNIT_TEST"
	_, err := harness.sender.Send(context.Background(), request)
	assertPermanent(t, err, TelegramCodeMissingCredential)
	if harness.hits.Load() != 0 {
		t.Fatal("nil resolver must fail closed before any network call")
	}
}

func TestTelegramHTTPStatusOverridesSuccessBody(t *testing.T) {
	cases := []struct {
		name   string
		status int
		extra  string
		assert func(t *testing.T, err error)
	}{
		{name: "500_with_ok_body", status: http.StatusInternalServerError,
			assert: func(t *testing.T, err error) { assertTransient(t, err, TelegramCodeProviderUnavailable) }},
		{name: "401_with_ok_body", status: http.StatusUnauthorized,
			assert: func(t *testing.T, err error) { assertPermanent(t, err, TelegramCodeUnauthorized) }},
		{name: "429_with_ok_body", status: http.StatusTooManyRequests,
			assert: func(t *testing.T, err error) {
				transient := assertTransient(t, err, TelegramCodeRateLimited)
				if transient.RetryAfter != 0 && transient.RetryAfter > maxRetryAfterSeconds*time.Second {
					t.Fatal("retry after out of range")
				}
			}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(testCase.status)
				writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":123}}`+testCase.extra)
			})
			result, err := harness.sender.Send(context.Background(), testSendRequest())
			if result.ProviderMessageID != "" {
				t.Fatal("non-2xx must never return success")
			}
			testCase.assert(t, err)
		})
	}
}

func TestTelegramMalformedBodyClassifiedByStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header string
		assert func(t *testing.T, err error)
	}{
		{name: "malformed_429_uses_retry_after_header", status: http.StatusTooManyRequests, header: "7",
			assert: func(t *testing.T, err error) {
				transient := assertTransient(t, err, TelegramCodeRateLimited)
				if transient.RetryAfter != 7*time.Second {
					t.Fatalf("Retry-After header = %v, want 7s", transient.RetryAfter)
				}
			}},
		{name: "malformed_429_without_header", status: http.StatusTooManyRequests,
			assert: func(t *testing.T, err error) { assertTransient(t, err, TelegramCodeRateLimited) }},
		{name: "malformed_500", status: http.StatusInternalServerError,
			assert: func(t *testing.T, err error) { assertTransient(t, err, TelegramCodeProviderUnavailable) }},
		{name: "malformed_400", status: http.StatusBadRequest,
			assert: func(t *testing.T, err error) { assertPermanent(t, err, TelegramCodeInvalidDestination) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Retry-After", testCase.header)
				writer.WriteHeader(testCase.status)
				writer.Write([]byte(`<html>bad gateway</html>`))
			})
			_, err := harness.sender.Send(context.Background(), testSendRequest())
			testCase.assert(t, err)
		})
	}
}

func TestTelegramOversizedBodyClassifiedByStatus(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		writer.Write([]byte(strings.Repeat("x", maxTelegramResponseBytes+2048)))
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	assertPermanent(t, err, TelegramCodeUnauthorized)
}

func TestTelegramUnsupportedTemplateVersionFailsClosedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	for _, version := range []string{"", "telegram-emergency-security-alert-v2"} {
		request := testSendRequest()
		request.TemplateVersion = version
		_, err := harness.sender.Send(context.Background(), request)
		assertPermanent(t, err, TelegramCodeUnsupportedTemplate)
	}
	if harness.hits.Load() != 0 {
		t.Fatal("unsupported template version must not reach the provider")
	}
}

func TestTelegramInvalidPayloadKindFailsClosedBeforeHTTP(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
		writeTelegramJSON(writer, `{"ok":true,"result":{"message_id":1}}`)
	})
	for _, kind := range []string{"", "UNKNOWN"} {
		request := testSendRequest()
		request.Payload.Kind = kind
		_, err := harness.sender.Send(context.Background(), request)
		assertPermanent(t, err, TelegramCodeInvalidPayloadKind)
	}
	if harness.hits.Load() != 0 {
		t.Fatal("invalid payload kind must not reach the provider")
	}
}

func TestTelegramRetryAfterTrustRules(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		header        string
		expectedCode  string
		expectedRetry time.Duration
	}{
		{
			name:          "malformed_partial_json_falls_back_to_header",
			body:          `{"ok":false,"parameters":{"retry_after":300}`,
			header:        "7",
			expectedCode:  TelegramCodeRateLimited,
			expectedRetry: 7 * time.Second,
		},
		{
			name:          "valid_json_body_wins_over_header",
			body:          `{"ok":false,"error_code":429,"description":"retry","parameters":{"retry_after":10}}`,
			header:        "7",
			expectedCode:  TelegramCodeRateLimited,
			expectedRetry: 10 * time.Second,
		},
		{
			name:          "malformed_json_without_header_yields_zero",
			body:          `{"ok":false,"parameters":{"retry_after":300}`,
			header:        "",
			expectedCode:  TelegramCodeRateLimited,
			expectedRetry: 0,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Retry-After", testCase.header)
				writer.WriteHeader(http.StatusTooManyRequests)
				writer.Write([]byte(testCase.body))
			})
			_, err := harness.sender.Send(context.Background(), testSendRequest())
			transient := assertTransient(t, err, testCase.expectedCode)
			if transient.RetryAfter != testCase.expectedRetry {
				t.Fatalf("RetryAfter = %v, want %v", transient.RetryAfter, testCase.expectedRetry)
			}
		})
	}
}

func TestTelegramProviderDescriptionNeverRetained(t *testing.T) {
	reflectedDescription := fmt.Sprintf("Bad Request: bad token %s for chat %s", fakeTelegramToken, fakeTelegramChat)
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		payload := map[string]any{"ok": false, "error_code": 400, "description": reflectedDescription}
		encoded, _ := json.Marshal(payload)
		writer.Write(encoded)
	})
	_, err := harness.sender.Send(context.Background(), testSendRequest())
	permanent := assertPermanent(t, err, TelegramCodeInvalidDestination)
	if permanent.Detail != "" {
		t.Fatalf("provider description must not be retained, got detail %q", permanent.Detail)
	}
	formatted := fmt.Sprintf("%+v", err)
	marshaled := mustMarshalTelegram(t, err)
	for _, candidate := range []string{err.Error(), formatted, marshaled} {
		assertNoTokenLeak(t, candidate)
	}
}

func TestTelegramProviderMismatchIsPermanent(t *testing.T) {
	harness := newTelegramTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Error("no provider call expected")
	})
	request := testSendRequest()
	request.Provider = ProviderWhatsApp
	_, err := harness.sender.Send(context.Background(), request)
	assertPermanent(t, err, TelegramCodeProviderMismatch)
	if harness.hits.Load() != 0 {
		t.Fatal("mismatched provider must not reach the network")
	}
}

func uuidForTest() string {
	return fmt.Sprintf("delivery-%d", time.Now().UnixNano())
}

func writeTelegramJSON(writer http.ResponseWriter, payload string) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write([]byte(payload))
}

func rawTelegramTokenForTest() string {
	return "123456789:" + strings.Repeat("Z", 35)
}

func mustMarshalTelegram(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fakeWhatsAppToken   = "EAA_TEST_TOKEN_NOT_REAL_12345678901234567890"
	fakeWhatsAppPhoneID = "1338779312641336"
	fakeWhatsAppWABAID  = "1411793004134777"
	fakeWhatsAppTarget  = "+15555550123"
	fakeWhatsAppVideo   = "https://media.example.test/evidence/review-token.mp4"
	fakeWhatsAppAlertID = "11111111-1111-4111-8111-111111111111"
)

type whatsAppTestHarness struct {
	sender *WhatsAppSender
	hits   *atomic.Int32
}

func newWhatsAppTestSender(t *testing.T, handler http.HandlerFunc) whatsAppTestHarness {
	t.Helper()
	t.Setenv("WHATSAPP_ACCESS_TOKEN_UNIT_TEST", fakeWhatsAppToken)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	return whatsAppTestHarness{
		sender: NewWhatsAppSender(NewEnvCredentialResolver(), WhatsAppSenderOptions{BaseURL: server.URL}),
		hits:   &hits,
	}
}

func whatsAppConfig(testVideoURL string) json.RawMessage {
	config := map[string]any{
		"wabaId":           fakeWhatsAppWABAID,
		"templateName":     WhatsAppTemplateName,
		"templateLanguage": WhatsAppTemplateLanguage,
		"templateVersion":  WhatsAppTemplateVersion,
		"optIn": map[string]any{
			"capturedAt":    "2026-08-28T10:00:00Z",
			"source":        "OWNER_DASHBOARD",
			"policyVersion": "whatsapp-emergency-alerts-v1",
		},
	}
	if testVideoURL != "" {
		config["testVideoUrl"] = testVideoURL
	}
	encoded, _ := json.Marshal(config)
	return encoded
}

func whatsAppAlertRequest() SendRequest {
	payload := BuildAlertPayload(AlertNotificationInput{
		StoreID:       "store-1",
		StoreName:     "Liquor Store",
		StoreTimezone: "America/Chicago",
		AlertID:       fakeWhatsAppAlertID,
		AlertType:     "WEAPON_DETECTED",
		Severity:      "CRITICAL",
		DetectedAt:    time.Date(2026, 8, 24, 21, 42, 0, 0, time.UTC),
		CameraID:      "camera-1",
		CameraName:    "Whole store",
		EvidenceID:    "evidence-1",
	})
	payload.ReviewURL = fakeWhatsAppVideo
	return SendRequest{
		DeliveryID:         uuidForTest(),
		Provider:           ProviderWhatsApp,
		ProviderAccountRef: fakeWhatsAppPhoneID,
		DestinationRef:     fakeWhatsAppTarget,
		CredentialRef:      EnvCredentialScheme + "WHATSAPP_ACCESS_TOKEN_UNIT_TEST",
		Config:             whatsAppConfig(""),
		TemplateName:       WhatsAppTemplateName,
		TemplateLanguage:   WhatsAppTemplateLanguage,
		TemplateVersion:    WhatsAppTemplateVersion,
		Payload:            payload,
	}
}

func whatsAppTestRequest() SendRequest {
	request := whatsAppAlertRequest()
	request.Payload = BuildTestPayload(ProviderWhatsApp)
	request.Config = whatsAppConfig(fakeWhatsAppVideo)
	return request
}

func whatsAppLinkedAlertRequest() SendRequest {
	request := whatsAppAlertRequest()
	request.TemplateName = WhatsAppLinkedTemplateName
	request.TemplateVersion = WhatsAppLinkedTemplateVersion
	var config map[string]any
	_ = json.Unmarshal(request.Config, &config)
	config["templateName"] = WhatsAppLinkedTemplateName
	config["templateVersion"] = WhatsAppLinkedTemplateVersion
	request.Config, _ = json.Marshal(config)
	return request
}

func assertWhatsAppPermanent(t *testing.T, err error, expected string) *PermanentSendError {
	t.Helper()
	if err == nil {
		t.Fatal("expected permanent error")
	}
	var permanent *PermanentSendError
	if !errors.As(err, &permanent) {
		t.Fatalf("expected permanent error, got %T: %v", err, err)
	}
	if permanent.Code != expected {
		t.Fatalf("expected %s, got %s", expected, permanent.Code)
	}
	assertNoWhatsAppLeak(t, fmt.Sprintf("%+v", err))
	return permanent
}

func assertWhatsAppTransient(t *testing.T, err error, expected string) *TransientSendError {
	t.Helper()
	if err == nil {
		t.Fatal("expected transient error")
	}
	var transient *TransientSendError
	if !errors.As(err, &transient) {
		t.Fatalf("expected transient error, got %T: %v", err, err)
	}
	if transient.Code != expected {
		t.Fatalf("expected %s, got %s", expected, transient.Code)
	}
	assertNoWhatsAppLeak(t, fmt.Sprintf("%+v", err))
	return transient
}

func assertNoWhatsAppLeak(t *testing.T, text string) {
	t.Helper()
	for _, forbidden := range []string{fakeWhatsAppToken, fakeWhatsAppTarget, strings.TrimPrefix(fakeWhatsAppTarget, "+")} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("credential or destination leaked: %s", text)
		}
	}
}

func TestWhatsAppSenderProvider(t *testing.T) {
	if provider := NewWhatsAppSender(NewEnvCredentialResolver(), WhatsAppSenderOptions{}).Provider(); provider != ProviderWhatsApp {
		t.Fatalf("unexpected provider %s", provider)
	}
}

func TestWhatsAppSenderBuildsApprovedVideoTemplateRequest(t *testing.T) {
	var captured whatsAppTemplateRequest
	var capturedURL string
	harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		capturedURL = request.URL.String()
		if request.Method != http.MethodPost || request.URL.Path != "/"+fakeWhatsAppPhoneID+"/messages" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+fakeWhatsAppToken {
			t.Fatal("missing bearer authorization")
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"messaging_product":"whatsapp","messages":[{"id":"wamid.test-42","message_status":"accepted"}]}`)
	})
	result, err := harness.sender.Send(context.Background(), whatsAppAlertRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderMessageID != "wamid.test-42" || result.ResponseMetadata["providerStatus"] != "accepted" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if captured.MessagingProduct != "whatsapp" || captured.Type != "template" || captured.To != strings.TrimPrefix(fakeWhatsAppTarget, "+") {
		t.Fatalf("unexpected envelope: %#v", captured)
	}
	if captured.Template.Name != WhatsAppTemplateName || captured.Template.Language.Code != WhatsAppTemplateLanguage {
		t.Fatalf("unexpected template contract: %#v", captured.Template)
	}
	if len(captured.Template.Components) != 2 {
		t.Fatalf("expected header and body, got %#v", captured.Template.Components)
	}
	header := captured.Template.Components[0]
	if header.Type != "header" || len(header.Parameters) != 1 || header.Parameters[0].Type != "video" || header.Parameters[0].Video == nil || header.Parameters[0].Video.Link != fakeWhatsAppVideo {
		t.Fatalf("unexpected video header: %#v", header)
	}
	body := captured.Template.Components[1]
	if body.Type != "body" || len(body.Parameters) != 4 {
		t.Fatalf("unexpected body parameters: %#v", body)
	}
	want := []string{"Liquor Store", "Whole store", "Aug 24, 2026, 4:42 PM CDT", "Possible violence or weapon detected"}
	for index, expected := range want {
		if body.Parameters[index].Type != "text" || body.Parameters[index].Text != expected {
			t.Fatalf("parameter %d: expected %q, got %#v", index+1, expected, body.Parameters[index])
		}
	}
	encoded, _ := json.Marshal(captured)
	if strings.Contains(string(encoded), fakeWhatsAppToken) || strings.Contains(capturedURL, fakeWhatsAppToken) {
		t.Fatal("access token must not be present in URL or JSON body")
	}
}

func TestWhatsAppSenderBuildsLinkedAlertTemplateRequest(t *testing.T) {
	var captured whatsAppTemplateRequest
	harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(writer, `{"messages":[{"id":"wamid.linked"}]}`)
	})
	if _, err := harness.sender.Send(context.Background(), whatsAppLinkedAlertRequest()); err != nil {
		t.Fatal(err)
	}
	if captured.Template.Name != WhatsAppLinkedTemplateName || captured.Template.Language.Code != WhatsAppTemplateLanguage {
		t.Fatalf("unexpected linked template contract: %#v", captured.Template)
	}
	if len(captured.Template.Components) != 3 {
		t.Fatalf("expected header, body and URL button, got %#v", captured.Template.Components)
	}
	button := captured.Template.Components[2]
	if button.Type != "button" || button.SubType != "url" || button.Index != "0" || len(button.Parameters) != 1 || button.Parameters[0].Type != "text" || button.Parameters[0].Text != fakeWhatsAppAlertID {
		t.Fatalf("unexpected alert URL button: %#v", button)
	}
}

func TestWhatsAppTestPayloadUsesConfiguredVideoAndClearTestCopy(t *testing.T) {
	var captured whatsAppTemplateRequest
	harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewDecoder(request.Body).Decode(&captured)
		_, _ = io.WriteString(writer, `{"messages":[{"id":"wamid.test"}]}`)
	})
	if _, err := harness.sender.Send(context.Background(), whatsAppTestRequest()); err != nil {
		t.Fatal(err)
	}
	if captured.Template.Components[0].Parameters[0].Video.Link != fakeWhatsAppVideo {
		t.Fatal("test media URL was not used")
	}
	values := captured.Template.Components[1].Parameters
	if values[0].Text != "Ketch Enterprise AI test" || values[3].Text != "Test notification - no real emergency" {
		t.Fatalf("test notification is not clearly identified: %#v", values)
	}
}

func TestWhatsAppSenderRejectsInvalidInputsBeforeHTTP(t *testing.T) {
	harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Fatal("HTTP must not be called for invalid input")
	})
	tests := []struct {
		name string
		code string
		edit func(*SendRequest)
	}{
		{"provider", WhatsAppCodeProviderMismatch, func(r *SendRequest) { r.Provider = ProviderTelegram }},
		{"template version", WhatsAppCodeUnsupportedTemplate, func(r *SendRequest) { r.TemplateVersion = "v2" }},
		{"template name", WhatsAppCodeInvalidTemplateName, func(r *SendRequest) { r.TemplateName = "other" }},
		{"template language", WhatsAppCodeInvalidTemplateLang, func(r *SendRequest) { r.TemplateLanguage = "vi" }},
		{"payload kind", WhatsAppCodeInvalidPayloadKind, func(r *SendRequest) { r.Payload.Kind = "OTHER" }},
		{"phone id", WhatsAppCodeInvalidConfiguration, func(r *SendRequest) { r.ProviderAccountRef = "phone-id" }},
		{"destination", WhatsAppCodeInvalidConfiguration, func(r *SendRequest) { r.DestinationRef = "555" }},
		{"template config", WhatsAppCodeInvalidConfiguration, func(r *SendRequest) { r.Config = json.RawMessage(`{"token":"secret"}`) }},
		{"missing video", WhatsAppCodeMissingVideo, func(r *SendRequest) { r.Payload.ReviewURL = "" }},
		{"http video", WhatsAppCodeInvalidVideoURL, func(r *SendRequest) { r.Payload.ReviewURL = "http://media.example.test/video.mp4" }},
		{"private video", WhatsAppCodeInvalidVideoURL, func(r *SendRequest) { r.Payload.ReviewURL = "https://127.0.0.1/video.mp4" }},
		{"single-label video host", WhatsAppCodeInvalidVideoURL, func(r *SendRequest) { r.Payload.ReviewURL = "https://intranet/video.mp4" }},
		{"credential query", WhatsAppCodeInvalidVideoURL, func(r *SendRequest) { r.Payload.ReviewURL = "https://media.example.test/video.mp4?access_token=value" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := whatsAppAlertRequest()
			test.edit(&request)
			_, err := harness.sender.Send(context.Background(), request)
			assertWhatsAppPermanent(t, err, test.code)
		})
	}
	linkedRequest := whatsAppLinkedAlertRequest()
	linkedRequest.Payload.AlertID = "../other-alert?admin=true"
	_, linkedErr := harness.sender.Send(context.Background(), linkedRequest)
	assertWhatsAppPermanent(t, linkedErr, WhatsAppCodeInvalidTemplateParams)
	if harness.hits.Load() != 0 {
		t.Fatalf("unexpected HTTP calls: %d", harness.hits.Load())
	}
}

func TestWhatsAppSenderCredentialFailures(t *testing.T) {
	request := whatsAppAlertRequest()
	request.CredentialRef = "render-secret://whatsapp/token"
	sender := NewWhatsAppSender(NewEnvCredentialResolver(), WhatsAppSenderOptions{})
	_, err := sender.Send(context.Background(), request)
	assertWhatsAppPermanent(t, err, WhatsAppCodeUnsupportedCredRef)

	request.CredentialRef = EnvCredentialScheme + "WHATSAPP_TOKEN_NOT_SET"
	_, err = sender.Send(context.Background(), request)
	assertWhatsAppPermanent(t, err, WhatsAppCodeMissingCredential)

	sender = NewWhatsAppSender(nil, WhatsAppSenderOptions{})
	request.CredentialRef = EnvCredentialScheme + "WHATSAPP_ACCESS_TOKEN_UNIT_TEST"
	_, err = sender.Send(context.Background(), request)
	assertWhatsAppPermanent(t, err, WhatsAppCodeMissingCredential)
}

func TestWhatsAppSenderBlocksRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Location", target.URL)
				writer.WriteHeader(status)
			})
			_, err := harness.sender.Send(context.Background(), whatsAppAlertRequest())
			assertWhatsAppTransient(t, err, WhatsAppCodeInvalidResponse)
		})
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d requests", targetHits.Load())
	}
}

func TestWhatsAppSenderHasFiniteRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_, _ = io.WriteString(writer, `{"messages":[{"id":"too-late"}]}`)
	}))
	defer server.Close()
	t.Setenv("WHATSAPP_ACCESS_TOKEN_UNIT_TEST", fakeWhatsAppToken)
	sender := NewWhatsAppSender(NewEnvCredentialResolver(), WhatsAppSenderOptions{
		BaseURL: server.URL, HTTPClient: &http.Client{Timeout: 0}, RequestTimeout: 50 * time.Millisecond,
	})
	started := time.Now()
	_, err := sender.Send(context.Background(), whatsAppAlertRequest())
	assertWhatsAppTransient(t, err, WhatsAppCodeNetworkError)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("request was not bounded: %s", elapsed)
	}
}

func TestWhatsAppHTTPAndGraphErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		code       string
		transient  bool
	}{
		{"http rate limit", 429, `{}`, "7", WhatsAppCodeRateLimited, true},
		{"http rate limit overrides graph body", 429, `{"error":{"code":190}}`, "7", WhatsAppCodeRateLimited, true},
		{"provider unavailable", 503, `{}`, "", WhatsAppCodeProviderUnavailable, true},
		{"unauthorized", 401, `{}`, "", WhatsAppCodeUnauthorized, false},
		{"forbidden", 403, `{}`, "", WhatsAppCodeForbidden, false},
		{"account missing", 404, `{}`, "", WhatsAppCodeAccountNotFound, false},
		{"graph rate limit", 400, `{"error":{"code":130429}}`, "9", WhatsAppCodeRateLimited, true},
		{"graph token", 400, `{"error":{"code":190}}`, "", WhatsAppCodeUnauthorized, false},
		{"graph destination", 400, `{"error":{"code":131026}}`, "", WhatsAppCodeInvalidDestination, false},
		{"graph payment eligibility", 400, `{"error":{"code":131042}}`, "", WhatsAppCodePaymentEligibility, false},
		{"graph parameters", 400, `{"error":{"code":132000}}`, "", WhatsAppCodeInvalidTemplateParams, false},
		{"graph template missing", 400, `{"error":{"code":132001}}`, "", WhatsAppCodeTemplateNotFound, false},
		{"graph template paused", 400, `{"error":{"code":132015}}`, "", WhatsAppCodeTemplateUnavailable, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
				if test.retryAfter != "" {
					writer.Header().Set("Retry-After", test.retryAfter)
				}
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			})
			_, err := harness.sender.Send(context.Background(), whatsAppAlertRequest())
			if test.transient {
				got := assertWhatsAppTransient(t, err, test.code)
				if test.retryAfter != "" && got.RetryAfter != time.Duration(mustInt(test.retryAfter))*time.Second {
					t.Fatalf("unexpected retry delay %s", got.RetryAfter)
				}
			} else {
				assertWhatsAppPermanent(t, err, test.code)
			}
		})
	}
}

func TestWhatsAppSenderRejectsMalformedOversizedAndIncompleteSuccess(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed", `{"messages":`},
		{"oversized", strings.Repeat("x", maxWhatsAppResponseBytes+1)},
		{"missing messages", `{}`},
		{"missing message id", `{"messages":[{"id":""}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.WriteString(writer, test.body)
			})
			_, err := harness.sender.Send(context.Background(), whatsAppAlertRequest())
			assertWhatsAppTransient(t, err, WhatsAppCodeInvalidResponse)
		})
	}
}

func TestWhatsAppProviderDescriptionNeverLeaks(t *testing.T) {
	harness := newWhatsAppTestSender(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(writer, `{"error":{"code":131026,"message":"token %s target %s"}}`, fakeWhatsAppToken, fakeWhatsAppTarget)
	})
	_, err := harness.sender.Send(context.Background(), whatsAppAlertRequest())
	permanent := assertWhatsAppPermanent(t, err, WhatsAppCodeInvalidDestination)
	if permanent.Detail != "" {
		t.Fatal("provider description must not be retained")
	}
	encoded, _ := json.Marshal(permanent)
	assertNoWhatsAppLeak(t, string(encoded))
	assertNoWhatsAppLeak(t, err.Error())
}

func mustInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liquor-store/security-api/internal/notifications"
)

func TestValidateEndpointTextFields(t *testing.T) {
	if err := validateEndpointTextFields(strPtr("Owner Telegram"), nil, strPtr("12345"), strPtr("env://TELEGRAM_BOT_TOKEN")); err != nil {
		t.Fatalf("valid endpoint fields rejected: %v", err)
	}
	if err := validateEndpointTextFields(strPtr(""), nil, strPtr("12345"), strPtr("env://TOKEN")); err == nil || !strings.Contains(err.Error(), "label") {
		t.Fatalf("empty label must be rejected, got %v", err)
	}
	if err := validateEndpointTextFields(strPtr(strings.Repeat("x", 121)), nil, nil, nil); err == nil {
		t.Fatal("overlong label must be rejected")
	}
	if err := validateEndpointTextFields(nil, strPtr(strings.Repeat("x", 201)), nil, nil); err == nil {
		t.Fatal("overlong providerAccountRef must be rejected")
	}
	if err := validateEndpointTextFields(nil, nil, strPtr("   "), nil); err == nil {
		t.Fatal("blank destinationRef must be rejected")
	}
	rawTelegramToken := "123456789:" + strings.Repeat("A", 35)
	for _, badCredential := range []string{"", "TELEGRAM_BOT_TOKEN", rawTelegramToken, "EAA" + strings.Repeat("b", 25), "https://secrets.example/token", "env://" + rawTelegramToken} {
		if err := validateEndpointTextFields(nil, nil, strPtr("chat"), strPtr(badCredential)); err == nil {
			t.Fatalf("credentialRef %q must be rejected", badCredential)
		}
	}
}

func TestNormalizeAlertTypesValidation(t *testing.T) {
	normalized, message := normalizeAlertTypes([]string{"weapon_detected", " WEAPON_DETECTED ", "POS_VOID_OR_REFUND"})
	if message != "" {
		t.Fatalf("unexpected error: %s", message)
	}
	if len(normalized) != 2 || normalized[0] != "WEAPON_DETECTED" || normalized[1] != "POS_VOID_OR_REFUND" {
		t.Fatalf("duplicates must normalize preserving order: %v", normalized)
	}
	if _, message = normalizeAlertTypes([]string{"NOT_A_TYPE"}); message == "" {
		t.Fatal("unknown alert type must be rejected")
	}
	if _, message = normalizeAlertTypes(nil); message != "" {
		t.Fatalf("nil alert types are valid: %s", message)
	}
}

func TestResolveRuleCreateAppliesDefaults(t *testing.T) {
	resolved, err := resolveRuleCreate(notificationRuleInput{Name: strPtr("Emergency alerts")})
	if err != nil {
		t.Fatalf("minimal create rejected: %v", err)
	}
	if resolved.MinimumSeverity != "CRITICAL" || resolved.AlertTypes == nil || len(resolved.AlertTypes) != 0 || resolved.CooldownSeconds != 0 || !resolved.IsEnabled {
		t.Fatalf("unexpected defaults: %+v", resolved)
	}
	severity := "HIGH"
	cooldown := 600
	disabled := false
	resolved, err = resolveRuleCreate(notificationRuleInput{
		Name: strPtr("Custom"), MinimumSeverity: &severity,
		AlertTypes: &[]string{"WEAPON_DETECTED", " WEAPON_DETECTED "}, CooldownSeconds: &cooldown, IsEnabled: &disabled,
	})
	if err != nil {
		t.Fatalf("explicit create rejected: %v", err)
	}
	if resolved.MinimumSeverity != "HIGH" || len(resolved.AlertTypes) != 1 || resolved.CooldownSeconds != 600 || resolved.IsEnabled {
		t.Fatalf("explicit values not honored: %+v", resolved)
	}
	if _, err = resolveRuleCreate(notificationRuleInput{}); err == nil {
		t.Fatal("missing name must be rejected")
	}
	badSeverity := "EXTREME"
	if _, err = resolveRuleCreate(notificationRuleInput{Name: strPtr("X"), MinimumSeverity: &badSeverity}); err == nil {
		t.Fatal("invalid severity must be rejected")
	}
	negativeCooldown := -1
	if _, err = resolveRuleCreate(notificationRuleInput{Name: strPtr("X"), CooldownSeconds: &negativeCooldown}); err == nil {
		t.Fatal("negative cooldown must be rejected")
	}
}

func TestApplyRuleUpdateStaysPartial(t *testing.T) {
	current := resolvedRule{Name: "Existing", MinimumSeverity: "HIGH", AlertTypes: []string{"WEAPON_DETECTED"}, CooldownSeconds: 300, IsEnabled: true}
	merged, err := applyRuleUpdate(current, notificationRuleInput{Name: strPtr("Renamed")})
	if err != nil {
		t.Fatalf("partial update rejected: %v", err)
	}
	if merged.Name != "Renamed" {
		t.Fatalf("name not updated: %+v", merged)
	}
	if merged.MinimumSeverity != "HIGH" || len(merged.AlertTypes) != 1 || merged.CooldownSeconds != 300 || !merged.IsEnabled {
		t.Fatalf("PATCH applied create defaults to omitted fields: %+v", merged)
	}
	emptyName := "   "
	if _, err = applyRuleUpdate(current, notificationRuleInput{Name: &emptyName}); err == nil {
		t.Fatal("blank name update must be rejected")
	}
	hugeCooldown := 86401
	if _, err = applyRuleUpdate(current, notificationRuleInput{CooldownSeconds: &hugeCooldown}); err == nil {
		t.Fatal("cooldown above 86400 must be rejected")
	}
	badSeverity := "EXTREME"
	if _, err = applyRuleUpdate(current, notificationRuleInput{MinimumSeverity: &badSeverity}); err == nil {
		t.Fatal("invalid severity must be rejected")
	}
	if _, err = applyRuleUpdate(current, notificationRuleInput{AlertTypes: &[]string{"NOPE"}}); err == nil {
		t.Fatal("invalid alert type must be rejected")
	}
}

func TestNotificationInputsRejectEmptyPatchBodies(t *testing.T) {
	endpointInput := notificationEndpointInput{}
	if endpointInput.provided() {
		t.Fatal("empty endpoint PATCH body must count as not provided")
	}
	ruleInput := notificationRuleInput{}
	if ruleInput.provided() {
		t.Fatal("empty rule PATCH body must count as not provided")
	}
	channelInput := notificationRuleChannelInput{}
	if channelInput.provided() {
		t.Fatal("empty channel PATCH body must count as not provided")
	}
	enabledTrue := true
	channelInput.IsEnabled = &enabledTrue
	if !channelInput.provided() {
		t.Fatal("channel input with a field must count as provided")
	}
}

func TestUnknownPatchFieldsAreRejectedByDecoder(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/stores/00000000-0000-0000-0000-000000000000/notification-endpoints/00000000-0000-0000-0000-000000000001", strings.NewReader(`{"provider":"WHATSAPP"}`))
	request.Header.Set("Content-Type", "application/json")

	var input notificationEndpointUpdateInput
	if decodeJSON(recorder, request, &input) {
		t.Error("provider field should have been rejected as unknown")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider field, got %d", recorder.Code)
	}

	channelRecorder := httptest.NewRecorder()
	channelRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/stores/00000000-0000-0000-0000-000000000000/notification-rules/00000000-0000-0000-0000-000000000003/channels/00000000-0000-0000-0000-000000000004", strings.NewReader(`{"endpointId":"00000000-0000-0000-0000-000000000002"}`))
	channelRequest.Header.Set("Content-Type", "application/json")

	var channelInput notificationRuleChannelUpdateInput
	if decodeJSON(channelRecorder, channelRequest, &channelInput) {
		t.Error("endpointId field should have been rejected as unknown")
	}
	if channelRecorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown endpointId field, got %d", channelRecorder.Code)
	}
}

func TestWhatsAppEnableGate(t *testing.T) {
	optIn := `{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"whatsapp-emergency-alerts-v1"}`
	completeConfig := `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":` + optIn + `}`
	if err := validateEndpointEnableGate("WHATSAPP", "111122223333", "+15551234567", true, json.RawMessage(completeConfig)); err != nil {
		t.Fatalf("complete WhatsApp config rejected: %v", err)
	}
	linkedConfig := `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v2","optIn":` + optIn + `}`
	if err := validateEndpointEnableGate("WHATSAPP", "111122223333", "+15551234567", true, json.RawMessage(linkedConfig)); err != nil {
		t.Fatalf("linked WhatsApp config rejected: %v", err)
	}
	if err := validateEndpointEnableGate("WHATSAPP", "", "+15551234567", true, json.RawMessage(completeConfig)); err == nil {
		t.Fatal("missing Meta Phone Number ID must fail the gate")
	}
	if err := validateEndpointEnableGate("WHATSAPP", "111122223333", "15551234567", true, json.RawMessage(completeConfig)); err == nil {
		t.Fatal("non-E.164 destination must fail the gate")
	}
	for _, mutation := range []struct {
		name   string
		config string
	}{
		{"missing wabaId", `{"templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":` + optIn + `}`},
		{"wrong templateName", `{"wabaId":"111","templateName":"other_template","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":` + optIn + `}`},
		{"wrong language", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"pt_BR","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":` + optIn + `}`},
		{"wrong version", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v9","optIn":` + optIn + `}`},
		{"missing version", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","optIn":` + optIn + `}`},
		{"missing optIn", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1"}`},
		{"bad capturedAt", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"yesterday","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`},
		{"missing source", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","policyVersion":"v1"}}`},
		{"missing policyVersion", `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD"}}`},
	} {
		if err := validateEndpointEnableGate("WHATSAPP", "111122223333", "+15551234567", true, json.RawMessage(mutation.config)); err == nil {
			t.Fatalf("%s must fail the enable gate", mutation.name)
		}
	}
	if err := validateEndpointEnableGate("WHATSAPP", "111122223333", "+15551234567", false, json.RawMessage("{}")); err != nil {
		t.Fatalf("disabled WhatsApp endpoints need no gate: %v", err)
	}
	if err := validateEndpointEnableGate("TELEGRAM", "", "", true, json.RawMessage("{}")); err != nil {
		t.Fatalf("telegram endpoints need no gate: %v", err)
	}
}

func TestTemplateVersionResolutionForEndpoints(t *testing.T) {
	if notifications.ResolveTemplateVersion(notifications.ProviderTelegram, nil) != "telegram-emergency-security-alert-v1" {
		t.Fatal("telegram template version default changed unexpectedly")
	}
	if notifications.ResolveTemplateVersion(notifications.ProviderWhatsApp, nil) != "whatsapp-emergency-security-alert-v2" {
		t.Fatal("whatsapp template version default changed unexpectedly")
	}
}

func TestNotificationRoutesRequireAuthenticationAndRegistration(t *testing.T) {
	storeID := "00000000-0000-0000-0000-000000000001"
	endpointID := "00000000-0000-0000-0000-000000000002"
	ruleID := "00000000-0000-0000-0000-000000000003"
	channelID := "00000000-0000-0000-0000-000000000004"
	deliveryID := "00000000-0000-0000-0000-000000000005"
	alertID := "00000000-0000-0000-0000-000000000006"
	evidenceID := "00000000-0000-0000-0000-000000000007"
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/stores/" + storeID + "/notification-endpoints"},
		{http.MethodPost, "/api/v1/stores/" + storeID + "/notification-endpoints"},
		{http.MethodPatch, "/api/v1/stores/" + storeID + "/notification-endpoints/" + endpointID},
		{http.MethodDelete, "/api/v1/stores/" + storeID + "/notification-endpoints/" + endpointID},
		{http.MethodPost, "/api/v1/stores/" + storeID + "/notification-endpoints/" + endpointID + "/test"},
		{http.MethodGet, "/api/v1/stores/" + storeID + "/notification-rules"},
		{http.MethodPost, "/api/v1/stores/" + storeID + "/notification-rules"},
		{http.MethodPatch, "/api/v1/stores/" + storeID + "/notification-rules/" + ruleID},
		{http.MethodDelete, "/api/v1/stores/" + storeID + "/notification-rules/" + ruleID},
		{http.MethodGet, "/api/v1/stores/" + storeID + "/notification-rules/" + ruleID + "/channels"},
		{http.MethodPost, "/api/v1/stores/" + storeID + "/notification-rules/" + ruleID + "/channels"},
		{http.MethodPatch, "/api/v1/stores/" + storeID + "/notification-rules/" + ruleID + "/channels/" + channelID},
		{http.MethodDelete, "/api/v1/stores/" + storeID + "/notification-rules/" + ruleID + "/channels/" + channelID},
		{http.MethodGet, "/api/v1/stores/" + storeID + "/notification-deliveries"},
		{http.MethodGet, "/api/v1/stores/" + storeID + "/notification-deliveries/" + deliveryID},
		{http.MethodGet, "/api/v1/stores/" + storeID + "/alerts/" + alertID + "/evidence/" + evidenceID + "/playback-url"},
	}
	handler := newTestHandler()
	if len(routes) != 16 {
		t.Fatalf("expected 16 protected notification/runtime operations, got %d", len(routes))
	}
	for _, route := range routes {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(route.method, route.path, nil)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401 without token, got %d", route.method, route.path, recorder.Code)
		}
	}
}

func TestOpenAPIDocumentsNotificationPaths(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/docs-json", nil)
	newTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
		Tags  []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Schemas map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	for _, path := range []string{
		"/api/v1/stores/{storeId}/notification-endpoints",
		"/api/v1/stores/{storeId}/notification-endpoints/{endpointId}",
		"/api/v1/stores/{storeId}/notification-endpoints/{endpointId}/test",
		"/api/v1/stores/{storeId}/notification-rules",
		"/api/v1/stores/{storeId}/notification-rules/{ruleId}",
		"/api/v1/stores/{storeId}/notification-rules/{ruleId}/channels",
		"/api/v1/stores/{storeId}/notification-rules/{ruleId}/channels/{channelId}",
		"/api/v1/stores/{storeId}/notification-deliveries",
		"/api/v1/stores/{storeId}/notification-deliveries/{deliveryId}",
		"/api/v1/stores/{storeId}/alerts/{alertId}/evidence/{evidenceId}/playback-url",
		"/api/v1/notification-review/{token}",
		"/api/v1/webhooks/whatsapp",
	} {
		if _, ok := document.Paths[path]; !ok {
			t.Fatalf("openapi.json is missing path %s", path)
		}
	}

	var components struct {
		Schemas map[string]struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"schemas"`
	}
	var rawComponents struct {
		Components json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &rawComponents); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawComponents.Components, &components); err != nil {
		t.Fatal(err)
	}
	assertSchema := func(name string, requiredFields []string, forbiddenFields []string, mustExist bool) {
		schema, ok := components.Schemas[name]
		if !ok {
			if mustExist {
				t.Fatalf("missing schema %s", name)
			}
			return
		}
		for _, requiredField := range requiredFields {
			found := false
			for _, candidate := range schema.Required {
				if candidate == requiredField {
					found = true
				}
			}
			if !found {
				t.Fatalf("schema %s must require %s", name, requiredField)
			}
		}
		for _, forbiddenField := range forbiddenFields {
			if _, present := schema.Properties[forbiddenField]; present {
				t.Fatalf("schema %s must not allow field %s", name, forbiddenField)
			}
		}
	}
	assertSchema("NotificationEndpointCreateRequest", []string{"provider", "label", "destinationRef", "credentialRef"}, nil, true)
	assertSchema("NotificationEndpointUpdateRequest", nil, []string{"provider"}, true)
	assertSchema("NotificationRuleCreateRequest", []string{"name"}, nil, true)
	assertSchema("NotificationRuleUpdateRequest", nil, nil, true)
	assertSchema("NotificationRuleChannelCreateRequest", []string{"endpointId"}, nil, true)
	assertSchema("NotificationRuleChannelUpdateRequest", nil, []string{"endpointId"}, true)
	for _, stale := range []string{"NotificationEndpointRequest", "NotificationRuleRequest", "NotificationRuleChannelRequest"} {
		if _, ok := components.Schemas[stale]; ok {
			t.Fatalf("stale combined schema %s must be removed", stale)
		}
	}

	patchRef := func(path string) string {
		var operation struct {
			RequestBody *struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		}
		if err := json.Unmarshal(document.Paths[path]["patch"], &operation); err != nil {
			t.Fatalf("parse patch op %s: %v", path, err)
		}
		if operation.RequestBody == nil || len(operation.RequestBody.Content) == 0 {
			t.Fatalf("patch op %s has no request body", path)
		}
		return operation.RequestBody.Content["application/json"].Schema.Ref
	}
	expectedPatchRefs := map[string]string{
		"/api/v1/stores/{storeId}/notification-endpoints/{endpointId}":              "#/components/schemas/NotificationEndpointUpdateRequest",
		"/api/v1/stores/{storeId}/notification-rules/{ruleId}":                      "#/components/schemas/NotificationRuleUpdateRequest",
		"/api/v1/stores/{storeId}/notification-rules/{ruleId}/channels/{channelId}": "#/components/schemas/NotificationRuleChannelUpdateRequest",
	}
	for path, expected := range expectedPatchRefs {
		if got := patchRef(path); got != expected {
			t.Fatalf("patch %s references %q, want %q", path, got, expected)
		}
	}

	hasTag := false
	for _, tag := range document.Tags {
		if tag.Name == "notifications" {
			hasTag = true
		}
	}
	if !hasTag {
		t.Fatal("openapi.json is missing the notifications tag")
	}
}

func TestPublicReviewRouteFailsClosedWhenUnconfigured(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/notification-review/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO", nil)
	newTestHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for unconfigured secure review service, got %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("review bearer token leaked into response")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store, max-age=0" {
		t.Fatalf("review cache policy = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("review content-type policy = %q", got)
	}
}

func strPtr(value string) *string { return &value }

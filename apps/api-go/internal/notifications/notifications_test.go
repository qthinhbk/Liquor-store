package notifications

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSeverityRankOrdering(t *testing.T) {
	order := []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}
	for index, severity := range order {
		if SeverityRank(severity) != index+1 {
			t.Fatalf("%s rank = %d, want %d", severity, SeverityRank(severity), index+1)
		}
		if index > 0 && SeverityRank(severity) <= SeverityRank(order[index-1]) {
			t.Fatalf("%s does not outrank %s", severity, order[index-1])
		}
	}
	if SeverityRank("UNKNOWN") != 0 {
		t.Fatal("unknown severities must rank 0")
	}
	if !SeverityMeets("CRITICAL", "HIGH") {
		t.Fatal("CRITICAL must satisfy HIGH minimum")
	}
	if SeverityMeets("LOW", "MEDIUM") {
		t.Fatal("LOW must not satisfy MEDIUM minimum")
	}
	if SeverityMeets("BOGUS", "HIGH") || SeverityMeets("HIGH", "BOGUS") {
		t.Fatal("unknown severities must never match")
	}
}

func TestRuleMatching(t *testing.T) {
	rule := RuleSpec{ID: "rule-1", IsEnabled: true, MinimumSeverity: "CRITICAL", AlertTypes: []string{"WEAPON_DETECTED"}}
	if !RuleMatches(rule, "CRITICAL", "WEAPON_DETECTED") {
		t.Fatal("expected CRITICAL WEAPON_DETECTED to match")
	}
	if RuleMatches(rule, "HIGH", "WEAPON_DETECTED") {
		t.Fatal("severity below minimum must not match")
	}
	if RuleMatches(rule, "CRITICAL", "SUSPICIOUS_CASH_HANDLING") {
		t.Fatal("alert type outside list must not match")
	}
	wildcard := RuleSpec{ID: "rule-2", IsEnabled: true, MinimumSeverity: "HIGH"}
	if !RuleMatches(wildcard, "HIGH", "SUSPICIOUS_CASH_HANDLING") {
		t.Fatal("empty alertTypes must match any type passing severity")
	}
	disabled := rule
	disabled.IsEnabled = false
	if RuleMatches(disabled, "CRITICAL", "WEAPON_DETECTED") {
		t.Fatal("disabled rules must be excluded")
	}
}

func TestMatchingRulesFiltersAndPreservesOrder(t *testing.T) {
	rules := []RuleSpec{
		{ID: "a", IsEnabled: false, MinimumSeverity: "LOW"},
		{ID: "b", IsEnabled: true, MinimumSeverity: "HIGH", AlertTypes: []string{"WEAPON_DETECTED"}},
		{ID: "c", IsEnabled: true, MinimumSeverity: "CRITICAL", AlertTypes: []string{"POS_VOID_OR_REFUND"}},
	}
	matched := MatchingRules(rules, "CRITICAL", "WEAPON_DETECTED")
	if len(matched) != 1 || matched[0].ID != "b" {
		t.Fatalf("unexpected matches: %+v", matched)
	}
}

func TestAlertDedupeKeyPrefersCorrelationID(t *testing.T) {
	correlationKey := AlertDedupeKey(AlertReference("corr-1", "alert-1"), "rule", "endpoint")
	alertKey := AlertDedupeKey(AlertReference("", "alert-1"), "rule", "endpoint")
	if correlationKey != "alert:corr-1:rule:endpoint" {
		t.Fatalf("correlation key = %q", correlationKey)
	}
	if alertKey != "alert:alert-1:rule:endpoint" {
		t.Fatalf("alert fallback key = %q", alertKey)
	}
	sameIncidentOtherCamera := AlertDedupeKey(AlertReference("corr-1", "alert-2"), "rule", "endpoint")
	if sameIncidentOtherCamera != correlationKey {
		t.Fatal("same correlationId with a different alertId must share one dedupe identity")
	}
	versionedKey := AlertDedupeKey(AlertReference("corr-1", ""), "rule", "endpoint")
	if versionedKey != correlationKey {
		t.Fatal("templateVersion must not participate in the dedupe identity")
	}
	if strings.Contains(correlationKey, "v1") || strings.Count(correlationKey, ":") != 3 {
		t.Fatalf("dedupe key must have exactly incident/rule/endpoint segments, got %q", correlationKey)
	}
	independent := AlertDedupeKey(AlertReference("corr-2", "alert-2"), "rule", "endpoint")
	if independent == correlationKey {
		t.Fatal("independent incidents must produce different dedupe keys")
	}
	noCorrelationA := AlertDedupeKey(AlertReference("", "alert-a"), "rule", "endpoint")
	noCorrelationB := AlertDedupeKey(AlertReference("", "alert-b"), "rule", "endpoint")
	if noCorrelationA == noCorrelationB {
		t.Fatal("without correlationId, different alertIds must stay independent")
	}
}

func TestTestDedupeKey(t *testing.T) {
	key := TestDedupeKey("endpoint-1", "request-1")
	if key != "test:endpoint-1:request-1" {
		t.Fatalf("test dedupe key = %q", key)
	}
	if key == TestDedupeKey("endpoint-1", "request-2") {
		t.Fatal("different requestIds must differ")
	}
}

func TestResolveTemplateVersion(t *testing.T) {
	if ResolveTemplateVersion(ProviderTelegram, nil) != TelegramTemplateVersion {
		t.Fatal("telegram default version mismatch")
	}
	if ResolveTemplateVersion(ProviderWhatsApp, nil) != WhatsAppTemplateVersion {
		t.Fatal("whatsapp default version mismatch")
	}
	override := json.RawMessage(`{"templateVersion":"whatsapp-emergency-security-alert-v9"}`)
	if ResolveTemplateVersion(ProviderWhatsApp, override) != "whatsapp-emergency-security-alert-v9" {
		t.Fatal("whatsapp config override must win")
	}
	limited := ResolveTemplateVersion(ProviderWhatsApp, json.RawMessage(`{"templateVersion":"`+strings.Repeat("x", 150)+`"}`))
	if utf8Len(limited) != 100 {
		t.Fatalf("override must be length-limited to 100 runes, got %d", utf8Len(limited))
	}
}

func TestSanitizeText(t *testing.T) {
	got := SanitizeText("  Weapon\x00 detected \r\n near\t\tcounter  ", 200)
	if got != "Weapon detected near counter" {
		t.Fatalf("sanitize text = %q", got)
	}
	long := SanitizeText(strings.Repeat("á", 300), 10)
	if utf8Len(long) != 10 || !strings.HasSuffix(long, "á") {
		t.Fatalf("length limit broken or UTF-8 damaged: %q", long)
	}
}

func utf8Len(value string) int { return len([]rune(value)) }

func TestBuildAlertPayloadIsOwnerSafe(t *testing.T) {
	payload := BuildAlertPayload(AlertNotificationInput{
		StoreID:               "store-1",
		StoreName:             "Liquor Store",
		StoreTimezone:         "Asia/Ho_Chi_Minh",
		AlertID:               "alert-1",
		CorrelationID:         "corr-1",
		AlertType:             "WEAPON_DETECTED",
		Severity:              "CRITICAL",
		DetectedAt:            time.Date(2026, 8, 24, 9, 42, 0, 0, time.UTC),
		SubjectPersonCategory: "UNKNOWN",
		CameraID:              "camera-1",
		CameraName:            "Whole store",
		EvidenceID:            "evidence-1",
		StorageKey:            "evidence/alert-1/clip.mp4",
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"confidence", "rtsp://", "password", "token", "metadata", "http"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("payload contains forbidden %q: %s", forbidden, text)
		}
	}
	for _, forbidden := range []string{"storageKey", "evidence/alert-1/clip.mp4"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("payload leaks storage locator %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{`"alertId":"alert-1"`, `"evidenceId":"evidence-1"`, `"dashboardPath":"/#/alerts/alert-1"`, `Emergency security review`} {
		if !strings.Contains(text, required) {
			t.Fatalf("payload missing %s: %s", required, text)
		}
	}
	if !strings.Contains(payload.DetectedAt, "+07") {
		t.Fatalf("detectedAt not rendered in store timezone: %q", payload.DetectedAt)
	}
	fallback := BuildAlertPayload(AlertNotificationInput{
		AlertID:    "alert-2",
		StoreName:  "Store\nWith\r\nBreaks",
		DetectedAt: time.Date(2026, 8, 24, 9, 42, 0, 0, time.UTC),
	})
	if strings.Contains(fallback.StoreName, "\n") || strings.Contains(fallback.StoreName, "\r") {
		t.Fatalf("line breaks survived sanitization: %q", fallback.StoreName)
	}
}

func TestValidateConfigObject(t *testing.T) {
	if err := ValidateConfigObject(json.RawMessage(`{"parseMode":"HTML","attachThumbnail":false}`)); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := ValidateConfigObject(json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("non-object config must be rejected")
	}
	if err := ValidateConfigObject(json.RawMessage(`not-json`)); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	if err := ValidateConfigObject(json.RawMessage(`{"accessToken":"secret"}`)); err == nil {
		t.Fatal("forbidden keys must be rejected")
	}
	rawTelegramToken := "123456789:" + strings.Repeat("A", 35)
	if err := ValidateConfigObject([]byte(`{"note":"` + rawTelegramToken + `"}`)); err == nil {
		t.Fatal("telegram token-like values must be rejected")
	}
	if err := ValidateConfigObject([]byte(`{"note":"EAA` + strings.Repeat("b", 25) + `"}`)); err == nil {
		t.Fatal("meta token-like values must be rejected")
	}
	nested := `{"optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"v1"},"wabaId":"123","templateName":"emergency_security_alert","templateLanguage":"en_US"}`
	if err := ValidateConfigObject([]byte(nested)); err != nil {
		t.Fatalf("whatsapp-shaped config rejected: %v", err)
	}
}

func TestSanitizeConfigForResponseRedactsSecrets(t *testing.T) {
	input := []byte(`{"label":"ok","leak":"123456789:` + strings.Repeat("A", 35) + `"}`)
	sanitized := string(SanitizeConfigForResponse(input))
	if strings.Contains(sanitized, strings.Repeat("A", 35)) {
		t.Fatalf("response config still contains token-like value: %s", sanitized)
	}
	if !strings.Contains(sanitized, "[redacted]") {
		t.Fatalf("expected redaction marker: %s", sanitized)
	}
	if string(SanitizeConfigForResponse(nil)) != "{}" {
		t.Fatal("empty config must sanitize to {}")
	}
}

func TestValidCredentialRef(t *testing.T) {
	valid := []string{"env://TELEGRAM_BOT_TOKEN", "render-secret://telegram/main-bot", "render-secret://whatsapp/cloud-api/access-token"}
	for _, candidate := range valid {
		if !ValidCredentialRef(candidate) {
			t.Fatalf("%q should be valid", candidate)
		}
	}
	rawTelegramToken := "123456789:" + strings.Repeat("A", 35)
	invalid := []string{
		"",
		"TELEGRAM_BOT_TOKEN",
		"https://example.com/secret",
		"file:///etc/passwd",
		"env://",
		"env://" + rawTelegramToken,
		"env://EAA" + strings.Repeat("b", 25),
		rawTelegramToken,
		"EAA" + strings.Repeat("b", 25),
		"env://has space",
	}
	for _, candidate := range invalid {
		if ValidCredentialRef(candidate) {
			t.Fatalf("%q should be invalid", candidate)
		}
	}
}

func TestMaskDestination(t *testing.T) {
	cases := map[string]string{
		"12025550123": "••••0123",
		"@ownerchat":  "••••chat",
		"1234":        "••••",
		"":            "••••",
	}
	for input, expected := range cases {
		if got := MaskDestination(input); got != expected {
			t.Fatalf("MaskDestination(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestValidateWhatsAppEnableConfig(t *testing.T) {
	valid := `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"whatsapp-emergency-alerts-v1"}}`
	if err := ValidateWhatsAppEnableConfig("111122223333", "+15551234567", json.RawMessage(valid)); err != nil {
		t.Fatalf("valid WhatsApp enable config rejected: %v", err)
	}
	for _, badPhoneID := range []string{"", "abc", "11-11", "+111122223333", "111122223333444455555"} {
		if err := ValidateWhatsAppEnableConfig(badPhoneID, "+15551234567", json.RawMessage(valid)); err == nil {
			t.Fatalf("providerAccountRef %q must be rejected as a non-decimal or overlong identifier", badPhoneID)
		}
	}
	badWaba := strings.Replace(valid, `"wabaId":"111"`, `"wabaId":"waba-account"`, 1)
	if err := ValidateWhatsAppEnableConfig("111122223333", "+15551234567", json.RawMessage(badWaba)); err == nil {
		t.Fatal("non-decimal config.wabaId must be rejected")
	}
	missingWaba := strings.Replace(valid, `"wabaId":"111",`, ``, 1)
	if err := ValidateWhatsAppEnableConfig("111122223333", "+15551234567", json.RawMessage(missingWaba)); err == nil {
		t.Fatal("missing config.wabaId must be rejected")
	}
	if err := ValidateWhatsAppEnableConfig("", "+15551234567", json.RawMessage(valid)); err == nil {
		t.Fatal("missing Meta Phone Number ID must be rejected")
	}
	for _, destination := range []string{"15551234567", "+02345678901", "+1555"} {
		if err := ValidateWhatsAppEnableConfig("111122223333", destination, json.RawMessage(valid)); err == nil {
			t.Fatalf("destination %q must be rejected", destination)
		}
	}
	cases := map[string]string{
		"missing wabaId":        `{"templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`,
		"wrong templateName":    `{"wabaId":"111","templateName":"promo","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`,
		"wrong language":        `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"fr_FR","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`,
		"wrong version":         `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"other-v2","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`,
		"missing version":       `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`,
		"missing optIn":         `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1"}`,
		"bad capturedAt":        `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"08/24/2026","source":"OWNER_DASHBOARD","policyVersion":"v1"}}`,
		"missing source":        `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","policyVersion":"v1"}}`,
		"missing policyVersion": `{"wabaId":"111","templateName":"emergency_security_alert","templateLanguage":"en_US","templateVersion":"whatsapp-emergency-security-alert-v1","optIn":{"capturedAt":"2026-08-24T00:00:00Z","source":"OWNER_DASHBOARD"}}`,
	}
	for name, config := range cases {
		if err := ValidateWhatsAppEnableConfig("111122223333", "+15551234567", json.RawMessage(config)); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestFormatDetectedAtFallsBackToUTC(t *testing.T) {
	stamp := time.Date(2026, 8, 24, 9, 42, 0, 0, time.UTC)
	if got := FormatDetectedAt(stamp, ""); !strings.HasPrefix(got, "2026-08-24T09:42:00Z") {
		t.Fatalf("UTC fallback = %q", got)
	}
	if got := FormatDetectedAt(stamp, "Not/AZone"); !strings.HasPrefix(got, "2026-08-24T09:42:00Z") {
		t.Fatalf("invalid zone fallback = %q", got)
	}
}

func TestBuildTestPayloadHasNoPersonalData(t *testing.T) {
	encoded, err := json.Marshal(BuildTestPayload(ProviderTelegram))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"chat_id", "destinationref", "phonenumber", "@", "token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("test payload contains %q: %s", forbidden, text)
		}
	}
}

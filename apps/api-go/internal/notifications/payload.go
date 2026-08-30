package notifications

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxConfigBytes          = 4096
	maxConfigDepth          = 4
	maxConfigStringRunes    = 500
	maxTemplateVersionRunes = 100
)

var telegramTokenPattern = regexp.MustCompile(`^\d{6,12}:[A-Za-z0-9_-]{30,}$`)
var metaTokenPattern = regexp.MustCompile(`^EAA[A-Za-z0-9]{20,}$`)

var forbiddenConfigKeyParts = []string{"token", "secret", "password", "credential"}

func LooksLikeSecret(value string) bool {
	value = strings.TrimSpace(value)
	return telegramTokenPattern.MatchString(value) || metaTokenPattern.MatchString(value)
}

func SanitizeText(value string, maxLength int) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsControl(r) || r == utf8.RuneError {
			continue
		}
		builder.WriteRune(r)
	}
	result := strings.Join(strings.Fields(builder.String()), " ")
	if maxLength >= 0 {
		runes := []rune(result)
		if len(runes) > maxLength {
			result = string(runes[:maxLength])
		}
	}
	return result
}

func FormatDetectedAt(detectedAt time.Time, timezone string) string {
	name := SanitizeText(timezone, 64)
	if name != "" {
		if location, err := time.LoadLocation(name); err == nil {
			return detectedAt.In(location).Format("Jan 2, 2006, 3:04 PM MST")
		}
	}
	return detectedAt.UTC().Format(time.RFC3339)
}

func BuildAlertPayload(input AlertNotificationInput) RenderPayload {
	alertID := SanitizeText(input.AlertID, 64)
	payload := RenderPayload{
		Kind:                  DeliveryKindAlert,
		AlertID:               alertID,
		CorrelationID:         SanitizeText(input.CorrelationID, 128),
		AlertType:             SanitizeText(input.AlertType, 64),
		Severity:              SanitizeText(input.Severity, 16),
		Title:                 "Emergency security review",
		Description:           "A potential emergency was detected. Please review the footage and follow your store's emergency procedure.",
		StoreID:               SanitizeText(input.StoreID, 64),
		StoreName:             SanitizeText(input.StoreName, 120),
		CameraID:              SanitizeText(input.CameraID, 64),
		CameraName:            SanitizeText(input.CameraName, 120),
		DetectedAt:            FormatDetectedAt(input.DetectedAt, input.StoreTimezone),
		Timezone:              SanitizeText(input.StoreTimezone, 64),
		SubjectPersonCategory: SanitizeText(input.SubjectPersonCategory, 32),
		EvidenceID:            SanitizeText(input.EvidenceID, 64),
	}
	if alertID != "" {
		payload.DashboardPath = "/#/alerts?alertId=" + url.QueryEscape(alertID)
	}
	return payload
}

func BuildTestPayload(provider Provider) RenderPayload {
	return RenderPayload{
		Kind:        DeliveryKindTest,
		Provider:    string(provider),
		Title:       "Emergency security review",
		Description: "This is a Ketch Enterprise AI test notification. No real alert triggered it.",
		Message:     "If you received this message, the notification route is configured correctly.",
	}
}

type configIssue struct{ message string }

func (e configIssue) Error() string { return e.message }

func walkConfig(value any, depth int) error {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		if depth > maxConfigDepth {
			return configIssue{"config nesting is too deep."}
		}
		for key, child := range typed {
			lower := strings.ToLower(key)
			for _, part := range forbiddenConfigKeyParts {
				if strings.Contains(lower, part) {
					return configIssue{fmt.Sprintf("config must not contain %q keys.", part)}
				}
			}
			if err := walkConfig(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if depth > maxConfigDepth {
			return configIssue{"config nesting is too deep."}
		}
		for _, child := range typed {
			if err := walkConfig(child, depth+1); err != nil {
				return err
			}
		}
	case string:
		if utf8.RuneCountInString(typed) > maxConfigStringRunes {
			return configIssue{"config string values must be at most 500 characters."}
		}
		if LooksLikeSecret(typed) {
			return configIssue{"config must not contain token-like values."}
		}
	}
	return nil
}

func redactConfig(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = redactConfig(child)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = redactConfig(child)
		}
		return typed
	case string:
		if LooksLikeSecret(typed) {
			return "[redacted]"
		}
		return typed
	default:
		return value
	}
}

func ValidateConfigObject(raw json.RawMessage) error {
	trimmed := []byte(strings.TrimSpace(string(raw)))
	if len(trimmed) == 0 {
		return nil
	}
	if len(trimmed) > maxConfigBytes {
		return configIssue{"config must be at most 4096 bytes of JSON."}
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return configIssue{"config must be a JSON object."}
	}
	if _, ok := decoded.(map[string]any); !ok {
		return configIssue{"config must be a JSON object."}
	}
	return walkConfig(decoded, 1)
}

func SanitizeConfigForResponse(raw json.RawMessage) json.RawMessage {
	object, err := decodeConfigMap(raw)
	if err != nil {
		return json.RawMessage("{}")
	}
	encoded, err := json.Marshal(redactConfig(object))
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}

var credentialRefSchemes = []string{"env://", "render-secret://"}

func ValidCredentialRef(value string) bool {
	reference := strings.TrimSpace(value)
	if reference == "" || utf8.RuneCountInString(reference) > 200 {
		return false
	}
	if strings.ContainsAny(reference, " \t\r\n") {
		return false
	}
	matchedScheme := ""
	for _, scheme := range credentialRefSchemes {
		if strings.HasPrefix(reference, scheme) {
			matchedScheme = scheme
			break
		}
	}
	if matchedScheme == "" {
		return false
	}
	body := strings.TrimPrefix(reference, matchedScheme)
	if body == "" || LooksLikeSecret(body) || LooksLikeSecret(reference) {
		return false
	}
	return true
}

func MaskDestination(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 4 {
		return "••••"
	}
	return "••••" + string(runes[len(runes)-4:])
}

var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
var metaIdentifierPattern = regexp.MustCompile(`^\d{1,20}$`)

func ValidateWhatsAppEnableConfig(providerAccountRef, destinationRef string, config json.RawMessage) error {
	if !metaIdentifierPattern.MatchString(strings.TrimSpace(providerAccountRef)) {
		return configIssue{"providerAccountRef must be a non-empty decimal Meta Phone Number ID to enable a WhatsApp endpoint."}
	}
	if !e164Pattern.MatchString(strings.TrimSpace(destinationRef)) {
		return configIssue{"destinationRef must be an E.164 phone number such as +15551234567 to enable a WhatsApp endpoint."}
	}
	parsed, err := decodeConfigMap(config)
	if err != nil {
		return err
	}
	for _, key := range []string{"wabaId", "templateName", "templateLanguage", "templateVersion"} {
		value, _ := parsed[key].(string)
		if strings.TrimSpace(value) == "" {
			return configIssue{fmt.Sprintf("config.%s is required to enable a WhatsApp endpoint.", key)}
		}
	}
	wabaID, _ := parsed["wabaId"].(string)
	if !metaIdentifierPattern.MatchString(strings.TrimSpace(wabaID)) {
		return configIssue{"config.wabaId must be a non-empty decimal WhatsApp Business Account ID."}
	}
	templateName, _ := parsed["templateName"].(string)
	templateLanguage, _ := parsed["templateLanguage"].(string)
	templateVersion, _ := parsed["templateVersion"].(string)
	contract, supported := whatsAppContractForVersion(strings.TrimSpace(templateVersion))
	if !supported {
		return configIssue{fmt.Sprintf("config.templateVersion must be %s or %s.", WhatsAppTemplateVersion, WhatsAppLinkedTemplateVersion)}
	}
	if strings.TrimSpace(templateName) != contract.Name {
		return configIssue{fmt.Sprintf("config.templateName must be %s for %s.", contract.Name, contract.Version)}
	}
	if strings.TrimSpace(templateLanguage) != contract.Language {
		return configIssue{fmt.Sprintf("config.templateLanguage must be %s.", contract.Language)}
	}
	rawOptIn, present := parsed["optIn"]
	if !present {
		return configIssue{"config.optIn consent evidence is required to enable a WhatsApp endpoint."}
	}
	optIn, ok := rawOptIn.(map[string]any)
	if !ok {
		return configIssue{"config.optIn must be an object with consent evidence."}
	}
	capturedAtRaw, _ := optIn["capturedAt"].(string)
	source, _ := optIn["source"].(string)
	policyVersion, _ := optIn["policyVersion"].(string)
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(capturedAtRaw)); err != nil {
		return configIssue{"config.optIn.capturedAt must be an RFC3339 timestamp."}
	}
	if strings.TrimSpace(source) == "" || strings.TrimSpace(policyVersion) == "" {
		return configIssue{"config.optIn must include source and policyVersion consent evidence."}
	}
	return nil
}

func decodeConfigMap(raw json.RawMessage) (map[string]any, error) {
	trimmed := []byte(strings.TrimSpace(string(raw)))
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil || decoded == nil {
		return nil, configIssue{"config must be a JSON object."}
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, configIssue{"config must be a JSON object."}
	}
	return object, nil
}

func ResolveTemplateVersion(provider Provider, config json.RawMessage) string {
	fallback := ""
	switch provider {
	case ProviderTelegram:
		fallback = TelegramTemplateVersion
	case ProviderWhatsApp:
		fallback = WhatsAppDefaultTemplateVersion
	default:
		return ""
	}
	parsed, err := decodeConfigMap(config)
	if err != nil {
		return fallback
	}
	override, _ := parsed["templateVersion"].(string)
	override = SanitizeText(override, maxTemplateVersionRunes)
	if override != "" && !LooksLikeSecret(override) {
		return override
	}
	return fallback
}

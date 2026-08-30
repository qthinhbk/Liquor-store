package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	WhatsAppCodeNetworkError          = "whatsapp_network_error"
	WhatsAppCodeRateLimited           = "whatsapp_rate_limited"
	WhatsAppCodeProviderUnavailable   = "whatsapp_provider_unavailable"
	WhatsAppCodeInvalidResponse       = "whatsapp_invalid_response"
	WhatsAppCodeMissingCredential     = "whatsapp_missing_credential"
	WhatsAppCodeUnsupportedCredRef    = "whatsapp_unsupported_credential_ref"
	WhatsAppCodeInvalidDestination    = "whatsapp_invalid_destination"
	WhatsAppCodeUnauthorized          = "whatsapp_unauthorized"
	WhatsAppCodeForbidden             = "whatsapp_forbidden"
	WhatsAppCodeAccountNotFound       = "whatsapp_account_not_found"
	WhatsAppCodeInvalidRequest        = "whatsapp_invalid_request"
	WhatsAppCodeProviderMismatch      = "whatsapp_provider_mismatch"
	WhatsAppCodeUnsupportedTemplate   = "whatsapp_unsupported_template_version"
	WhatsAppCodeInvalidTemplateName   = "whatsapp_invalid_template_name"
	WhatsAppCodeInvalidTemplateLang   = "whatsapp_invalid_template_language"
	WhatsAppCodeInvalidPayloadKind    = "whatsapp_invalid_payload_kind"
	WhatsAppCodeInvalidConfiguration  = "whatsapp_invalid_configuration"
	WhatsAppCodeMissingVideo          = "whatsapp_missing_video"
	WhatsAppCodeInvalidVideoURL       = "whatsapp_invalid_video_url"
	WhatsAppCodeTemplateNotFound      = "whatsapp_template_not_found"
	WhatsAppCodeInvalidTemplateParams = "whatsapp_invalid_template_parameters"
	WhatsAppCodeTemplateUnavailable   = "whatsapp_template_unavailable"

	DefaultWhatsAppBaseURL   = "https://graph.facebook.com/v25.0"
	DefaultWhatsAppTimeout   = 15 * time.Second
	maxWhatsAppResponseBytes = 64 * 1024
	maxWhatsAppRetrySeconds  = 3600
)

type WhatsAppSenderOptions struct {
	BaseURL        string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

type WhatsAppSender struct {
	httpClient     *http.Client
	baseURL        string
	resolver       CredentialResolver
	requestTimeout time.Duration
}

func NewWhatsAppSender(resolver CredentialResolver, options WhatsAppSenderOptions) *WhatsAppSender {
	injected := options.HTTPClient
	if injected == nil {
		injected = &http.Client{}
	}
	cloned := *injected
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = DefaultWhatsAppTimeout
	}
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultWhatsAppBaseURL
	}
	return &WhatsAppSender{
		httpClient:     &cloned,
		baseURL:        baseURL,
		resolver:       resolver,
		requestTimeout: requestTimeout,
	}
}

func (s *WhatsAppSender) Provider() Provider { return ProviderWhatsApp }

type whatsAppTemplateParameter struct {
	Type  string                    `json:"type"`
	Text  string                    `json:"text,omitempty"`
	Video *whatsAppTemplateVideoRef `json:"video,omitempty"`
}

type whatsAppTemplateVideoRef struct {
	Link string `json:"link"`
}

type whatsAppTemplateComponent struct {
	Type       string                      `json:"type"`
	SubType    string                      `json:"sub_type,omitempty"`
	Index      string                      `json:"index,omitempty"`
	Parameters []whatsAppTemplateParameter `json:"parameters"`
}

type whatsAppTemplateRequest struct {
	MessagingProduct string `json:"messaging_product"`
	RecipientType    string `json:"recipient_type"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Template         struct {
		Name     string `json:"name"`
		Language struct {
			Code string `json:"code"`
		} `json:"language"`
		Components []whatsAppTemplateComponent `json:"components"`
	} `json:"template"`
}

type whatsAppAPIResponse struct {
	Messages []struct {
		ID            string `json:"id"`
		MessageStatus string `json:"message_status"`
	} `json:"messages"`
	Error *struct {
		Code         int `json:"code"`
		ErrorSubcode int `json:"error_subcode"`
	} `json:"error"`
}

type whatsAppEndpointConfig struct {
	WABAID           string `json:"wabaId"`
	TemplateName     string `json:"templateName"`
	TemplateLanguage string `json:"templateLanguage"`
	TemplateVersion  string `json:"templateVersion"`
	TestVideoURL     string `json:"testVideoUrl"`
}

func (s *WhatsAppSender) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if request.Provider != ProviderWhatsApp {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeProviderMismatch}
	}
	if err := ctx.Err(); err != nil {
		return SendResult{}, &TransientSendError{Code: WhatsAppCodeNetworkError}
	}
	contract, supported := whatsAppContractForVersion(request.TemplateVersion)
	if !supported {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeUnsupportedTemplate}
	}
	if request.TemplateName != contract.Name {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidTemplateName}
	}
	if request.TemplateLanguage != contract.Language {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidTemplateLang}
	}
	switch request.Payload.Kind {
	case DeliveryKindAlert, DeliveryKindTest:
	default:
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidPayloadKind}
	}
	if err := ValidateConfigObject(request.Config); err != nil {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidConfiguration}
	}
	if err := ValidateWhatsAppEnableConfig(request.ProviderAccountRef, request.DestinationRef, request.Config); err != nil {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidConfiguration}
	}
	var config whatsAppEndpointConfig
	if err := json.Unmarshal(request.Config, &config); err != nil {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidConfiguration}
	}
	if config.TemplateName != request.TemplateName || config.TemplateLanguage != request.TemplateLanguage || config.TemplateVersion != request.TemplateVersion {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidConfiguration}
	}
	if contract.HasAlertButton && request.Payload.Kind == DeliveryKindAlert {
		alertID := SanitizeText(request.Payload.AlertID, 128)
		if _, err := uuid.Parse(alertID); err != nil {
			return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidTemplateParams}
		}
	}
	videoURL := strings.TrimSpace(request.Payload.ReviewURL)
	if request.Payload.Kind == DeliveryKindTest && videoURL == "" {
		videoURL = strings.TrimSpace(config.TestVideoURL)
	}
	if videoURL == "" {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeMissingVideo}
	}
	if !validPublicHTTPSURL(videoURL) {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidVideoURL}
	}
	if strings.TrimSpace(request.CredentialRef) == "" || s.resolver == nil {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeMissingCredential}
	}
	token, resolveErr := s.resolver.Resolve(ctx, request.CredentialRef)
	if resolveErr != nil {
		var permanent *PermanentSendError
		if errors.As(resolveErr, &permanent) {
			if permanent.Code == CredentialCodeUnsupportedScheme {
				return SendResult{}, &PermanentSendError{Code: WhatsAppCodeUnsupportedCredRef}
			}
			return SendResult{}, &PermanentSendError{Code: WhatsAppCodeMissingCredential}
		}
		return SendResult{}, &TransientSendError{Code: WhatsAppCodeNetworkError}
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeMissingCredential}
	}

	requestBody, err := json.Marshal(buildWhatsAppTemplateRequest(request, videoURL))
	if err != nil {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidRequest}
	}
	sendCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	endpointURL := s.baseURL + "/" + strings.TrimSpace(request.ProviderAccountRef) + "/messages"
	httpRequest, err := http.NewRequestWithContext(sendCtx, http.MethodPost, endpointURL, bytes.NewReader(requestBody))
	if err != nil {
		return SendResult{}, &PermanentSendError{Code: WhatsAppCodeInvalidRequest}
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := s.httpClient.Do(httpRequest)
	if err != nil {
		return SendResult{}, &TransientSendError{Code: WhatsAppCodeNetworkError}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxWhatsAppResponseBytes+1))
	oversized := len(raw) > maxWhatsAppResponseBytes
	var api whatsAppAPIResponse
	parseErr := json.Unmarshal(raw, &api)
	validJSON := readErr == nil && !oversized && parseErr == nil
	statusIsSuccess := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	if !statusIsSuccess {
		return SendResult{}, classifyWhatsAppHTTPStatus(response.StatusCode, response.Header.Get("Retry-After"), &api, validJSON)
	}
	providerMessageID := ""
	if len(api.Messages) > 0 {
		providerMessageID = SanitizeText(api.Messages[0].ID, 200)
	}
	if !validJSON || providerMessageID == "" {
		return SendResult{}, &TransientSendError{Code: WhatsAppCodeInvalidResponse}
	}
	metadata := map[string]any{"httpStatus": response.StatusCode}
	if status := SanitizeText(api.Messages[0].MessageStatus, 100); status != "" {
		metadata["providerStatus"] = status
	}
	return SendResult{ProviderMessageID: providerMessageID, ResponseMetadata: metadata}, nil
}

func buildWhatsAppTemplateRequest(request SendRequest, videoURL string) whatsAppTemplateRequest {
	bodyValues := whatsAppBodyValues(request.Payload)
	contract, _ := whatsAppContractForVersion(request.TemplateVersion)
	var payload whatsAppTemplateRequest
	payload.MessagingProduct = "whatsapp"
	payload.RecipientType = "individual"
	payload.To = strings.TrimPrefix(strings.TrimSpace(request.DestinationRef), "+")
	payload.Type = "template"
	payload.Template.Name = request.TemplateName
	payload.Template.Language.Code = request.TemplateLanguage
	payload.Template.Components = []whatsAppTemplateComponent{
		{
			Type: "header",
			Parameters: []whatsAppTemplateParameter{{
				Type:  "video",
				Video: &whatsAppTemplateVideoRef{Link: videoURL},
			}},
		},
		{
			Type: "body",
			Parameters: []whatsAppTemplateParameter{
				{Type: "text", Text: bodyValues[0]},
				{Type: "text", Text: bodyValues[1]},
				{Type: "text", Text: bodyValues[2]},
				{Type: "text", Text: bodyValues[3]},
			},
		},
	}
	if contract.HasAlertButton {
		alertID := SanitizeText(request.Payload.AlertID, 128)
		if alertID == "" && request.Payload.Kind == DeliveryKindTest {
			alertID = uuid.Nil.String()
		}
		payload.Template.Components = append(payload.Template.Components, whatsAppTemplateComponent{
			Type: "button", SubType: "url", Index: "0",
			Parameters: []whatsAppTemplateParameter{{Type: "text", Text: alertID}},
		})
	}
	return payload
}

func whatsAppBodyValues(payload RenderPayload) [4]string {
	storeName := SanitizeText(payload.StoreName, 120)
	cameraName := SanitizeText(payload.CameraName, 120)
	detectedAt := SanitizeText(payload.DetectedAt, 120)
	event := whatsAppEventLabel(payload)
	if payload.Kind == DeliveryKindTest {
		if storeName == "" {
			storeName = "Ketch Enterprise AI test"
		}
		if cameraName == "" {
			cameraName = "Test camera"
		}
		if detectedAt == "" {
			detectedAt = "Test delivery"
		}
	}
	if storeName == "" {
		storeName = "Your store"
	}
	if cameraName == "" {
		cameraName = "Unknown camera"
	}
	if detectedAt == "" {
		detectedAt = "Time unavailable"
	}
	return [4]string{storeName, cameraName, detectedAt, event}
}

func whatsAppEventLabel(payload RenderPayload) string {
	if payload.Kind == DeliveryKindTest {
		return "Test notification - no real emergency"
	}
	switch strings.ToUpper(SanitizeText(payload.AlertType, 64)) {
	case "WEAPON_DETECTED", "VIOLENCE_OR_WEAPON_DETECTED":
		return "Possible violence or weapon detected"
	default:
		return "Potential emergency security event"
	}
}

func validPublicHTTPSURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 2048 {
		return false
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") {
		return false
	}
	if ip := net.ParseIP(hostname); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return false
		}
	} else if !strings.Contains(hostname, ".") {
		return false
	}
	for key := range parsed.Query() {
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"token", "secret", "password", "credential", "access_key"} {
			if strings.Contains(lower, forbidden) {
				return false
			}
		}
	}
	return true
}

func classifyWhatsAppHTTPStatus(status int, retryAfterHeader string, api *whatsAppAPIResponse, haveValidJSON bool) error {
	retryAfter := parseWhatsAppRetryAfter(retryAfterHeader, time.Now())
	switch {
	case status == http.StatusTooManyRequests:
		return &TransientSendError{Code: WhatsAppCodeRateLimited, RetryAfter: retryAfter}
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		return &TransientSendError{Code: WhatsAppCodeProviderUnavailable, RetryAfter: retryAfter}
	case status >= http.StatusMultipleChoices && status < http.StatusBadRequest:
		return &TransientSendError{Code: WhatsAppCodeInvalidResponse}
	case status == http.StatusUnauthorized:
		return &PermanentSendError{Code: WhatsAppCodeUnauthorized}
	case status == http.StatusForbidden:
		return &PermanentSendError{Code: WhatsAppCodeForbidden}
	case status == http.StatusNotFound:
		return &PermanentSendError{Code: WhatsAppCodeAccountNotFound}
	case status == http.StatusBadRequest:
		if haveValidJSON && api != nil && api.Error != nil {
			if classified := classifyWhatsAppGraphError(api.Error.Code, api.Error.ErrorSubcode, retryAfter); classified != nil {
				return classified
			}
		}
		return &PermanentSendError{Code: WhatsAppCodeInvalidRequest}
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return &PermanentSendError{Code: WhatsAppCodeInvalidRequest}
	default:
		return &TransientSendError{Code: WhatsAppCodeProviderUnavailable}
	}
}

func classifyWhatsAppGraphError(code, subcode int, retryAfter time.Duration) error {
	effective := code
	if effective == 0 {
		effective = subcode
	}
	switch effective {
	case 1, 2, 4, 17, 341, 368, 80007, 131000:
		return &TransientSendError{Code: WhatsAppCodeProviderUnavailable, RetryAfter: retryAfter}
	case 130429, 131048, 131056:
		return &TransientSendError{Code: WhatsAppCodeRateLimited, RetryAfter: retryAfter}
	case 190:
		return &PermanentSendError{Code: WhatsAppCodeUnauthorized}
	case 10, 200:
		return &PermanentSendError{Code: WhatsAppCodeForbidden}
	case 131026, 131030:
		return &PermanentSendError{Code: WhatsAppCodeInvalidDestination}
	case 132000:
		return &PermanentSendError{Code: WhatsAppCodeInvalidTemplateParams}
	case 132001:
		return &PermanentSendError{Code: WhatsAppCodeTemplateNotFound}
	case 132015, 132016:
		return &PermanentSendError{Code: WhatsAppCodeTemplateUnavailable}
	default:
		return nil
	}
}

func parseWhatsAppRetryAfter(value string, now time.Time) time.Duration {
	header := strings.TrimSpace(value)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds > 0 && seconds <= maxWhatsAppRetrySeconds {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	parsed, err := http.ParseTime(header)
	if err != nil {
		return 0
	}
	delay := parsed.Sub(now)
	if delay <= 0 || delay > maxWhatsAppRetrySeconds*time.Second {
		return 0
	}
	return delay
}

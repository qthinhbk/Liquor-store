package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	TelegramCodeNetworkError        = "telegram_network_error"
	TelegramCodeRateLimited         = "telegram_rate_limited"
	TelegramCodeProviderUnavailable = "telegram_provider_unavailable"
	TelegramCodeInvalidResponse     = "telegram_invalid_response"
	TelegramCodeMissingCredential   = "telegram_missing_credential"
	TelegramCodeUnsupportedCredRef  = "telegram_unsupported_credential_ref"
	TelegramCodeInvalidDestination  = "telegram_invalid_destination"
	TelegramCodeUnauthorized        = "telegram_unauthorized"
	TelegramCodeForbidden           = "telegram_forbidden"
	TelegramCodeDestinationNotFound = "telegram_destination_not_found"
	TelegramCodeInvalidRequest      = "telegram_invalid_request"
	TelegramCodeProviderMismatch    = "telegram_provider_mismatch"
	TelegramCodeUnsupportedTemplate = "telegram_unsupported_template_version"
	TelegramCodeInvalidPayloadKind  = "telegram_invalid_payload_kind"

	DefaultTelegramBaseURL   = "https://api.telegram.org"
	DefaultTelegramTimeout   = 10 * time.Second
	maxTelegramResponseBytes = 64 * 1024
	maxTelegramMessageRunes  = 4000
	maxRetryAfterSeconds     = 3600

	ParseModeHTML = "HTML"
)

type TelegramSenderOptions struct {
	BaseURL        string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

type TelegramSender struct {
	httpClient     *http.Client
	baseURL        string
	resolver       CredentialResolver
	requestTimeout time.Duration
}

func NewTelegramSender(resolver CredentialResolver, options TelegramSenderOptions) *TelegramSender {
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
		requestTimeout = DefaultTelegramTimeout
	}
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultTelegramBaseURL
	}
	return &TelegramSender{
		httpClient:     &cloned,
		baseURL:        baseURL,
		resolver:       resolver,
		requestTimeout: requestTimeout,
	}
}

func (s *TelegramSender) Provider() Provider { return ProviderTelegram }

type telegramSendMessageRequest struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	ParseMode             string `json:"parse_mode,omitempty"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type telegramAPIResponse struct {
	OK     bool `json:"ok"`
	Result *struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (s *TelegramSender) Send(ctx context.Context, request SendRequest) (SendResult, error) {
	if request.Provider != ProviderTelegram {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeProviderMismatch}
	}
	if err := ctx.Err(); err != nil {
		return SendResult{}, &TransientSendError{Code: TelegramCodeNetworkError}
	}
	if request.TemplateVersion != TelegramTemplateVersion {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeUnsupportedTemplate}
	}
	switch request.Payload.Kind {
	case DeliveryKindAlert, DeliveryKindTest:
	default:
		return SendResult{}, &PermanentSendError{Code: TelegramCodeInvalidPayloadKind}
	}
	destination := strings.TrimSpace(request.DestinationRef)
	if destination == "" {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeInvalidDestination}
	}
	if strings.TrimSpace(request.CredentialRef) == "" {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeMissingCredential}
	}
	if s.resolver == nil {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeMissingCredential}
	}
	token, resolveErr := s.resolver.Resolve(ctx, request.CredentialRef)
	if resolveErr != nil {
		var permanent *PermanentSendError
		if errors.As(resolveErr, &permanent) {
			if permanent.Code == CredentialCodeUnsupportedScheme {
				return SendResult{}, &PermanentSendError{Code: TelegramCodeUnsupportedCredRef}
			}
			return SendResult{}, &PermanentSendError{Code: TelegramCodeMissingCredential}
		}
		return SendResult{}, &TransientSendError{Code: TelegramCodeNetworkError}
	}
	text, parseMode := renderTelegramMessage(request.Payload, request.Config)
	requestBody, marshalErr := json.Marshal(telegramSendMessageRequest{
		ChatID:                destination,
		Text:                  text,
		ParseMode:             parseMode,
		DisableWebPagePreview: true,
	})
	if marshalErr != nil {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeInvalidRequest}
	}
	sendCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	endpointURL := s.baseURL + "/bot" + token + "/sendMessage"
	httpRequest, requestErr := http.NewRequestWithContext(sendCtx, http.MethodPost, endpointURL, bytes.NewReader(requestBody))
	if requestErr != nil {
		return SendResult{}, &PermanentSendError{Code: TelegramCodeInvalidRequest}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, doErr := s.httpClient.Do(httpRequest)
	if doErr != nil {
		return SendResult{}, &TransientSendError{Code: TelegramCodeNetworkError}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxTelegramResponseBytes+1))
	oversized := len(raw) > maxTelegramResponseBytes
	var api telegramAPIResponse
	parseErr := json.Unmarshal(raw, &api)
	statusIsSuccess := response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	if !statusIsSuccess {
		return SendResult{}, classifyTelegramHTTPStatus(response.StatusCode, response.Header.Get("Retry-After"), &api, parseErr == nil && !oversized && readErr == nil)
	}
	if readErr != nil || oversized || parseErr != nil {
		return SendResult{}, &TransientSendError{Code: TelegramCodeInvalidResponse}
	}
	if !api.OK {
		return SendResult{}, classifyTelegramAPIError(&api)
	}
	if api.Result == nil || api.Result.MessageID == 0 {
		return SendResult{}, &TransientSendError{Code: TelegramCodeInvalidResponse}
	}
	return SendResult{
		ProviderMessageID: strconv.FormatInt(api.Result.MessageID, 10),
		ResponseMetadata:  map[string]any{"httpStatus": response.StatusCode},
	}, nil
}

func classifyTelegramHTTPStatus(status int, retryAfterHeader string, api *telegramAPIResponse, haveValidJSON bool) error {
	var retrySource *telegramAPIResponse
	if haveValidJSON {
		retrySource = api
	}
	retryAfter := extractRetryAfter(retrySource, retryAfterHeader)
	switch {
	case status == http.StatusTooManyRequests:
		return &TransientSendError{Code: TelegramCodeRateLimited, RetryAfter: retryAfter}
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		return &TransientSendError{Code: TelegramCodeProviderUnavailable}
	case status >= http.StatusMultipleChoices && status < http.StatusBadRequest:
		return &TransientSendError{Code: TelegramCodeInvalidResponse}
	case status == http.StatusBadRequest:
		return &PermanentSendError{Code: TelegramCodeInvalidDestination}
	case status == http.StatusUnauthorized:
		return &PermanentSendError{Code: TelegramCodeUnauthorized}
	case status == http.StatusForbidden:
		return &PermanentSendError{Code: TelegramCodeForbidden}
	case status == http.StatusNotFound:
		return &PermanentSendError{Code: TelegramCodeDestinationNotFound}
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return &PermanentSendError{Code: TelegramCodeInvalidRequest}
	default:
		return &TransientSendError{Code: TelegramCodeProviderUnavailable}
	}
}

func classifyTelegramAPIError(api *telegramAPIResponse) error {
	effective := api.ErrorCode
	retryAfter := extractRetryAfter(api, "")
	switch {
	case effective == http.StatusTooManyRequests:
		return &TransientSendError{Code: TelegramCodeRateLimited, RetryAfter: retryAfter}
	case effective == http.StatusRequestTimeout || effective >= http.StatusInternalServerError:
		return &TransientSendError{Code: TelegramCodeProviderUnavailable}
	case effective == http.StatusBadRequest:
		return &PermanentSendError{Code: TelegramCodeInvalidDestination}
	case effective == http.StatusUnauthorized:
		return &PermanentSendError{Code: TelegramCodeUnauthorized}
	case effective == http.StatusForbidden:
		return &PermanentSendError{Code: TelegramCodeForbidden}
	case effective == http.StatusNotFound:
		return &PermanentSendError{Code: TelegramCodeDestinationNotFound}
	case effective >= http.StatusBadRequest && effective < http.StatusInternalServerError:
		return &PermanentSendError{Code: TelegramCodeInvalidRequest}
	default:
		return &TransientSendError{Code: TelegramCodeProviderUnavailable}
	}
}

func extractRetryAfter(api *telegramAPIResponse, retryAfterHeader string) time.Duration {
	if api != nil && api.Parameters != nil && api.Parameters.RetryAfter > 0 && api.Parameters.RetryAfter <= maxRetryAfterSeconds {
		return time.Duration(api.Parameters.RetryAfter) * time.Second
	}
	header := strings.TrimSpace(retryAfterHeader)
	if header == "" {
		return 0
	}
	seconds, parseErr := strconv.Atoi(header)
	if parseErr != nil || seconds <= 0 || seconds > maxRetryAfterSeconds {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

const (
	telegramAlertHeader = "🚨 Potential emergency — review required"
	telegramTestHeader  = "✅ Test notification — Ketch Enterprise AI"
	telegramReviewNote  = "Please check the footage and follow your store's emergency procedure."
)

func telegramParseMode(config json.RawMessage) string {
	var parsed struct {
		ParseMode string `json:"parseMode"`
	}
	if len(config) > 0 && json.Unmarshal(config, &parsed) == nil {
		if strings.EqualFold(strings.TrimSpace(parsed.ParseMode), ParseModeHTML) {
			return ParseModeHTML
		}
	}
	return ""
}

func renderTelegramMessage(payload RenderPayload, config json.RawMessage) (string, string) {
	parseMode := telegramParseMode(config)
	renderValue := func(value string) string {
		value = SanitizeText(value, 300)
		if parseMode == ParseModeHTML {
			return html.EscapeString(value)
		}
		return value
	}
	var builder strings.Builder
	appendLine := func(label, value string) {
		if value == "" {
			return
		}
		builder.WriteString(label + value + "\n")
	}
	if payload.Kind == DeliveryKindTest {
		builder.WriteString(telegramTestHeader + "\n\n")
		builder.WriteString(renderValue(payload.Description) + "\n")
		appendLine("", renderValue(payload.Message))
	} else {
		builder.WriteString(telegramAlertHeader + "\n\n")
		appendLine("Store: ", renderValue(payload.StoreName))
		appendLine("Camera: ", renderValue(payload.CameraName))
		appendLine("Detected: ", renderValue(payload.DetectedAt))
		builder.WriteString("\n" + renderValue(payload.Description) + "\n")
		if strings.TrimSpace(payload.ReviewURL) != "" {
			if parseMode == ParseModeHTML {
				builder.WriteString(`<a href="` + html.EscapeString(payload.ReviewURL) + `">Review alert video</a>` + "\n")
			} else {
				appendLine("Review video: ", renderValue(payload.ReviewURL))
			}
		}
		builder.WriteString(telegramReviewNote + "\n")
	}
	message := builder.String()
	runes := []rune(message)
	if len(runes) > maxTelegramMessageRunes {
		message = string(runes[:maxTelegramMessageRunes])
	}
	return message, parseMode
}

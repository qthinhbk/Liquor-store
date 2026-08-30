package notifications

import (
	"context"
	"encoding/json"
	"time"
)

type SendRequest struct {
	DeliveryID         string
	Provider           Provider
	ProviderAccountRef string
	DestinationRef     string
	CredentialRef      string
	Config             json.RawMessage
	TemplateName       string
	TemplateLanguage   string
	TemplateVersion    string
	Payload            RenderPayload
}

type SendResult struct {
	ProviderMessageID string
	ResponseMetadata  map[string]any
}

type TransientSendError struct {
	Code       string
	RetryAfter time.Duration
}

func (e *TransientSendError) Error() string { return e.Code }

type PermanentSendError struct {
	Code   string
	Detail string
}

func (e *PermanentSendError) Error() string { return e.Code }

type Sender interface {
	Provider() Provider
	Send(ctx context.Context, request SendRequest) (SendResult, error)
}

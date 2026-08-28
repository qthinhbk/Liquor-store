package notifications

import (
	"time"

	_ "time/tzdata"
)

type Provider string

const (
	ProviderTelegram Provider = "TELEGRAM"
	ProviderWhatsApp Provider = "WHATSAPP"
)

const (
	DeliveryKindAlert = "ALERT"
	DeliveryKindTest  = "TEST"
)

const (
	StatusWaitingFallback = "WAITING_FALLBACK"
	StatusPending         = "PENDING"
	StatusProcessing      = "PROCESSING"
	StatusRetryScheduled  = "RETRY_SCHEDULED"
	StatusSent            = "SENT"
	StatusFailed          = "FAILED"
	StatusCancelled       = "CANCELLED"
)

const (
	TelegramTemplateVersion  = "telegram-emergency-security-alert-v1"
	WhatsAppTemplateVersion  = "whatsapp-emergency-security-alert-v1"
	WhatsAppTemplateName     = "emergency_security_alert"
	WhatsAppTemplateLanguage = "en_US"
	MaxAttemptsDefault       = 5
)

type AlertNotificationInput struct {
	StoreID               string
	StoreName             string
	StoreTimezone         string
	AlertID               string
	CorrelationID         string
	AlertType             string
	Severity              string
	DetectedAt            time.Time
	SubjectPersonCategory string
	CameraID              string
	CameraName            string
	EvidenceID            string
	StorageKey            string
	ThumbnailStorageKey   string
}

type RenderPayload struct {
	Kind                  string `json:"kind"`
	Provider              string `json:"provider,omitempty"`
	AlertID               string `json:"alertId,omitempty"`
	CorrelationID         string `json:"correlationId,omitempty"`
	AlertType             string `json:"alertType,omitempty"`
	Severity              string `json:"severity,omitempty"`
	Title                 string `json:"title"`
	Description           string `json:"description"`
	Message               string `json:"message,omitempty"`
	StoreID               string `json:"storeId,omitempty"`
	StoreName             string `json:"storeName,omitempty"`
	CameraID              string `json:"cameraId,omitempty"`
	CameraName            string `json:"cameraName,omitempty"`
	DetectedAt            string `json:"detectedAt,omitempty"`
	Timezone              string `json:"timezone,omitempty"`
	SubjectPersonCategory string `json:"subjectPersonCategory,omitempty"`
	DashboardPath         string `json:"dashboardPath,omitempty"`
	EvidenceID            string `json:"evidenceId,omitempty"`
	// ReviewURL is generated immediately before a provider call. It is never
	// serialized into the durable delivery payload.
	ReviewURL string `json:"-"`
}

type DeliverySummary struct {
	ID                   string `json:"id"`
	Kind                 string `json:"deliveryKind"`
	EndpointID           string `json:"endpointId,omitempty"`
	RuleID               string `json:"ruleId,omitempty"`
	Provider             string `json:"provider"`
	Priority             int    `json:"priority"`
	FallbackDelaySeconds int    `json:"fallbackDelaySeconds,omitempty"`
	Status               string `json:"status"`
	TemplateVersion      string `json:"templateVersion"`
}

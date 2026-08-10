package server

import (
	"encoding/json"
	"time"
)

type authUser struct {
	ID           string
	Email        string
	DisplayName  string
	PasswordHash string
	Status       string
}

type currentUser struct {
	ID          string       `json:"id"`
	Email       string       `json:"email"`
	DisplayName string       `json:"displayName"`
	Stores      []membership `json:"stores"`
}

type membership struct {
	StoreID   string `json:"storeId"`
	StoreName string `json:"storeName"`
	StoreCode string `json:"storeCode"`
	Role      string `json:"role"`
}

type storeResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Code      string     `json:"code"`
	Address   *string    `json:"address"`
	Timezone  string     `json:"timezone"`
	Role      string     `json:"role,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

type cameraResponse struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Location         string    `json:"location"`
	Protocol         string    `json:"protocol"`
	StreamGatewayRef string    `json:"streamGatewayRef"`
	Status           string    `json:"status"`
	IsEnabled        bool      `json:"isEnabled"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type zoneResponse struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	Kind                   string          `json:"kind"`
	ExpectedPersonCategory *string         `json:"expectedPersonCategory"`
	Polygon                json.RawMessage `json:"polygon"`
	DwellThresholdSeconds  *int            `json:"dwellThresholdSeconds"`
	IsEnabled              bool            `json:"isEnabled"`
}

type alertResponse struct {
	ID                    string          `json:"id"`
	SourceEventID         *string         `json:"sourceEventId"`
	CorrelationID         *string         `json:"correlationId"`
	Type                  string          `json:"type"`
	Severity              string          `json:"severity"`
	Status                string          `json:"status"`
	SubjectPersonCategory string          `json:"subjectPersonCategory"`
	Confidence            *float64        `json:"confidence"`
	DetectedAt            time.Time       `json:"detectedAt"`
	AcknowledgedAt        *time.Time      `json:"acknowledgedAt"`
	ResolutionNote        *string         `json:"resolutionNote"`
	Metadata              json.RawMessage `json:"metadata"`
	CameraID              *string         `json:"cameraId"`
	CameraName            *string         `json:"cameraName"`
	ZoneID                *string         `json:"zoneId"`
	ZoneName              *string         `json:"zoneName"`
}

type evidenceResponse struct {
	ID              string    `json:"id"`
	StorageKey      string    `json:"storageKey"`
	MimeType        string    `json:"mimeType"`
	DurationSeconds int       `json:"durationSeconds"`
	StartsAt        time.Time `json:"startsAt"`
	EndsAt          time.Time `json:"endsAt"`
}

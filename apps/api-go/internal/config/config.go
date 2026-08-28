package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Environment     string
	Port            string
	WebOrigin       string
	DatabaseURL     string
	DirectURL       string
	JWTAccessSecret string
	JWTAccessTTL    time.Duration
	JWTIssuer       string
	JWTAudience     string
	RefreshTTLDays  int
	RegisterEnabled bool
	MembersEnabled  bool
	SwaggerEnabled  bool
	TrustProxy      bool

	NotificationWorkerEnabled bool
	NotificationPollInterval  time.Duration
	NotificationLeaseDuration time.Duration
	NotificationBatchSize     int
	PublicAPIBaseURL          string
	SecureVideoLinkTTL        time.Duration
	EvidenceOriginBaseURL     string
	EvidenceOriginAuthToken   string
}

func Load() (Config, error) {
	// Local development may use apps/api-go/.env. Real process environment
	// variables always take precedence in production.
	_ = godotenv.Load(".env")

	ttl, err := time.ParseDuration(valueOr("JWT_ACCESS_TTL", "15m"))
	if err != nil || ttl <= 0 {
		return Config{}, errors.New("JWT_ACCESS_TTL must be a positive Go duration such as 15m")
	}
	refreshDays, err := strconv.Atoi(valueOr("REFRESH_TOKEN_TTL_DAYS", "30"))
	if err != nil || refreshDays < 1 {
		return Config{}, errors.New("REFRESH_TOKEN_TTL_DAYS must be a positive integer")
	}
	pollInterval, err := positiveDuration("NOTIFICATION_POLL_INTERVAL", "2s")
	if err != nil {
		return Config{}, err
	}
	leaseDuration, err := positiveDuration("NOTIFICATION_LEASE_DURATION", "45s")
	if err != nil {
		return Config{}, err
	}
	secureVideoTTL, err := positiveDuration("SECURE_VIDEO_LINK_TTL", "15m")
	if err != nil {
		return Config{}, err
	}
	batchSize, err := strconv.Atoi(valueOr("NOTIFICATION_BATCH_SIZE", "10"))
	if err != nil || batchSize < 1 || batchSize > 100 {
		return Config{}, errors.New("NOTIFICATION_BATCH_SIZE must be between 1 and 100")
	}

	environment := valueOr("NODE_ENV", "development")
	cfg := Config{
		Environment:     environment,
		Port:            valueOr("PORT", "3000"),
		WebOrigin:       valueOr("WEB_ORIGIN", "http://localhost:5173"),
		DatabaseURL:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		DirectURL:       strings.TrimSpace(os.Getenv("DIRECT_URL")),
		JWTAccessSecret: valueOr("JWT_ACCESS_SECRET", "development-only-change-me"),
		JWTAccessTTL:    ttl,
		JWTIssuer:       valueOr("JWT_ISSUER", "liquor-store-security-api"),
		JWTAudience:     valueOr("JWT_AUDIENCE", "liquor-store-owner-dashboard"),
		RefreshTTLDays:  refreshDays,
		RegisterEnabled: boolValue("REGISTER_ENABLED", false),
		MembersEnabled:  boolValue("MEMBER_MANAGEMENT_ENABLED", false),
		SwaggerEnabled:  boolValue("SWAGGER_ENABLED", environment != "production"),
		TrustProxy:      boolValue("TRUST_PROXY", false),

		NotificationWorkerEnabled: boolValue("NOTIFICATION_WORKER_ENABLED", false),
		NotificationPollInterval:  pollInterval,
		NotificationLeaseDuration: leaseDuration,
		NotificationBatchSize:     batchSize,
		PublicAPIBaseURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_API_BASE_URL")), "/"),
		SecureVideoLinkTTL:        secureVideoTTL,
		EvidenceOriginBaseURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("EVIDENCE_ORIGIN_BASE_URL")), "/"),
		EvidenceOriginAuthToken:   strings.TrimSpace(os.Getenv("EVIDENCE_ORIGIN_AUTH_TOKEN")),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.Environment == "production" {
		if len(cfg.JWTAccessSecret) < 32 {
			return Config{}, errors.New("JWT_ACCESS_SECRET must be at least 32 characters in production")
		}
		if cfg.JWTAccessSecret == "development-only-change-me" {
			return Config{}, errors.New("JWT_ACCESS_SECRET must not use the development default in production")
		}
		if strings.TrimSpace(cfg.WebOrigin) == "" {
			return Config{}, errors.New("WEB_ORIGIN is required in production")
		}
	}
	if cfg.NotificationWorkerEnabled && cfg.NotificationLeaseDuration <= 10*time.Second {
		return Config{}, errors.New("NOTIFICATION_LEASE_DURATION must be greater than 10s when the notification worker is enabled")
	}
	for name, value := range map[string]string{
		"PUBLIC_API_BASE_URL":      cfg.PublicAPIBaseURL,
		"EVIDENCE_ORIGIN_BASE_URL": cfg.EvidenceOriginBaseURL,
	} {
		if value == "" {
			continue
		}
		parsed, parseErr := url.Parse(value)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, fmt.Errorf("%s must be an absolute HTTP(S) origin without credentials, query, or fragment", name)
		}
		if cfg.Environment == "production" && parsed.Scheme != "https" {
			return Config{}, fmt.Errorf("%s must use HTTPS in production", name)
		}
	}
	return cfg, nil
}

func (c Config) Address() string { return fmt.Sprintf(":%s", c.Port) }

func (c Config) MigrationURL() string {
	if c.DirectURL != "" {
		return c.DirectURL
	}
	return c.DatabaseURL
}

func valueOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func boolValue(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func positiveDuration(name, fallback string) (time.Duration, error) {
	value := valueOr(name, fallback)
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return parsed, nil
}

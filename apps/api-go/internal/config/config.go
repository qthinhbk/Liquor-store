package config

import (
	"errors"
	"fmt"
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

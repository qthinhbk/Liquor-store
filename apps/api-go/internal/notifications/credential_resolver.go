package notifications

import (
	"context"
	"os"
	"strings"
)

const (
	EnvCredentialScheme          = "env://"
	RenderSecretCredentialScheme = "render-secret://"

	CredentialCodeUnsupportedScheme = "credential_unsupported_scheme"
	CredentialCodeMissing           = "credential_missing"
)

type CredentialResolver interface {
	Resolve(ctx context.Context, credentialRef string) (string, error)
}

type EnvCredentialResolver struct{}

func NewEnvCredentialResolver() *EnvCredentialResolver { return &EnvCredentialResolver{} }

func (r *EnvCredentialResolver) Resolve(ctx context.Context, credentialRef string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref := strings.TrimSpace(credentialRef)
	if ref == "" {
		return "", &PermanentSendError{Code: CredentialCodeMissing}
	}
	switch {
	case strings.HasPrefix(ref, EnvCredentialScheme):
		name := strings.TrimPrefix(ref, EnvCredentialScheme)
		if !isValidEnvironmentName(name) {
			return "", &PermanentSendError{Code: CredentialCodeUnsupportedScheme}
		}
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", &PermanentSendError{Code: CredentialCodeMissing}
		}
		return value, nil
	default:
		return "", &PermanentSendError{Code: CredentialCodeUnsupportedScheme}
	}
}

func isValidEnvironmentName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}

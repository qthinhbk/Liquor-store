package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	EnvCredentialScheme          = "env://"
	RenderSecretCredentialScheme = "render-secret://"

	CredentialCodeUnsupportedScheme = "credential_unsupported_scheme"
	CredentialCodeMissing           = "credential_missing"
	CredentialCodeNotAuthorized     = "credential_not_authorized"
)

type CredentialResolver interface {
	Resolve(ctx context.Context, request SendRequest) (string, error)
}

// Bindings are deployment configuration, never supplied by an API caller.
type CredentialBinding struct {
	StoreID            string   `json:"storeId"`
	Provider           Provider `json:"provider"`
	ProviderAccountRef string   `json:"providerAccountRef"`
	CredentialRef      string   `json:"credentialRef"`
}

func providerCredentialRef(provider Provider, ref string) bool {
	var prefix string
	switch provider {
	case ProviderTelegram:
		prefix = "TELEGRAM_BOT_TOKEN"
	case ProviderWhatsApp:
		prefix = "WHATSAPP_ACCESS_TOKEN"
	default:
		return false
	}
	if !strings.HasPrefix(ref, EnvCredentialScheme) {
		return false
	}
	name := strings.TrimPrefix(ref, EnvCredentialScheme)
	return isValidEnvironmentName(name) && (name == prefix || strings.HasPrefix(name, prefix+"_"))
}

func ParseCredentialBindings(raw string) ([]CredentialBinding, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var bindings []CredentialBinding
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bindings) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("NOTIFICATION_CREDENTIAL_BINDINGS must be a JSON array of credential bindings")
	}
	seen := map[CredentialBinding]bool{}
	for _, binding := range bindings {
		if _, err := uuid.Parse(binding.StoreID); err != nil || !providerCredentialRef(binding.Provider, binding.CredentialRef) || seen[binding] ||
			len(binding.ProviderAccountRef) > 200 || binding.ProviderAccountRef != strings.TrimSpace(binding.ProviderAccountRef) ||
			(binding.Provider == ProviderWhatsApp && strings.TrimSpace(binding.ProviderAccountRef) == "") {
			return nil, errors.New("NOTIFICATION_CREDENTIAL_BINDINGS contains an invalid or duplicate binding")
		}
		seen[binding] = true
	}
	return bindings, nil
}

func AuthorizeCredential(bindings []CredentialBinding, request SendRequest) error {
	ref := strings.TrimSpace(request.CredentialRef)
	if request.StoreID != "" && providerCredentialRef(request.Provider, ref) {
		for _, binding := range bindings {
			if binding.StoreID == request.StoreID && binding.Provider == request.Provider && binding.ProviderAccountRef == request.ProviderAccountRef && binding.CredentialRef == ref {
				return nil
			}
		}
	}
	return &PermanentSendError{Code: CredentialCodeNotAuthorized}
}

type EnvCredentialResolver struct{ bindings []CredentialBinding }

func NewEnvCredentialResolver(bindings ...CredentialBinding) *EnvCredentialResolver {
	return &EnvCredentialResolver{bindings: append([]CredentialBinding(nil), bindings...)}
}

func (r *EnvCredentialResolver) Resolve(ctx context.Context, request SendRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref := strings.TrimSpace(request.CredentialRef)
	if ref == "" {
		return "", &PermanentSendError{Code: CredentialCodeMissing}
	}
	switch {
	case strings.HasPrefix(ref, EnvCredentialScheme):
		name := strings.TrimPrefix(ref, EnvCredentialScheme)
		if !isValidEnvironmentName(name) {
			return "", &PermanentSendError{Code: CredentialCodeUnsupportedScheme}
		}
		if err := AuthorizeCredential(r.bindings, request); err != nil {
			return "", err
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

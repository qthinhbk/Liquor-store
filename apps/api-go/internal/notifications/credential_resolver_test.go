package notifications

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func isUnsupportedScheme(err error) bool {
	var permanent *PermanentSendError
	return errors.As(err, &permanent) && permanent.Code == CredentialCodeUnsupportedScheme
}

func TestEnvResolverResolvesTrimmedValue(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST_SET", "  123456789:TEST_VALUE  ")
	resolver := NewEnvCredentialResolver()
	value, err := resolver.Resolve(context.Background(), "env://TELEGRAM_BOT_TOKEN_UNIT_TEST_SET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "123456789:TEST_VALUE" {
		t.Fatalf("value = %q, want trimmed token", value)
	}
}

func TestEnvResolverMissingVariableIsPermanentMissingCredential(t *testing.T) {
	resolver := NewEnvCredentialResolver()
	_, err := resolver.Resolve(context.Background(), "env://TELEGRAM_BOT_TOKEN_DEFINITELY_UNSET_98765")
	var permanent *PermanentSendError
	if !errors.As(err, &permanent) || permanent.Code != CredentialCodeMissing {
		t.Fatalf("expected missing-credential permanent error, got %v", err)
	}
	assertNoTokenLeak(t, err.Error())
}

func TestEnvResolverEmptyVariableIsPermanentMissingCredential(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST_EMPTY", "   ")
	resolver := NewEnvCredentialResolver()
	_, err := resolver.Resolve(context.Background(), "env://TELEGRAM_BOT_TOKEN_UNIT_TEST_EMPTY")
	var permanent *PermanentSendError
	if !errors.As(err, &permanent) || permanent.Code != CredentialCodeMissing {
		t.Fatalf("empty variable must map to missing credential, got %v", err)
	}
}

func TestEnvResolverEmptyRefNameFailsClosed(t *testing.T) {
	resolver := NewEnvCredentialResolver()
	for _, ref := range []string{"env://", "env:// ", "env://HAS-DASH", "env://lower_case"} {
		if _, err := resolver.Resolve(context.Background(), ref); !isUnsupportedScheme(err) {
			t.Fatalf("ref %q must fail closed as unsupported scheme, got %v", ref, err)
		}
	}
}

func TestEnvResolverRenderSecretSchemeFailsClosed(t *testing.T) {
	resolver := NewEnvCredentialResolver()
	_, err := resolver.Resolve(context.Background(), RenderSecretCredentialScheme+"telegram/main-bot")
	if !isUnsupportedScheme(err) {
		t.Fatalf("render-secret scheme must fail closed until a deployment-specific resolver exists, got %v", err)
	}
}

func TestEnvResolverRejectsRawTokenWithoutScheme(t *testing.T) {
	resolver := NewEnvCredentialResolver()
	rawToken := rawTelegramTokenForTest()
	_, err := resolver.Resolve(context.Background(), rawToken)
	if !isUnsupportedScheme(err) {
		t.Fatalf("raw token without a scheme must be rejected, got %v", err)
	}
	if strings.Contains(err.Error(), rawToken) {
		t.Fatal("error output leaked the raw token")
	}
}

func TestEnvResolverEmptyCredentialRefIsMissing(t *testing.T) {
	resolver := NewEnvCredentialResolver()
	_, err := resolver.Resolve(context.Background(), "   ")
	var permanent *PermanentSendError
	if !errors.As(err, &permanent) || permanent.Code != CredentialCodeMissing {
		t.Fatalf("empty credentialRef must map to missing credential, got %v", err)
	}
}

func TestEnvResolverHonorsContextCancellation(t *testing.T) {
	resolver := NewEnvCredentialResolver()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.Resolve(ctx, "env://ANY_NAME"); err == nil {
		t.Fatal("cancelled context must short-circuit resolution")
	}
}

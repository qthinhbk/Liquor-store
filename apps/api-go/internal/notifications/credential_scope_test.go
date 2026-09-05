package notifications

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func newSenderTestResolver() *EnvCredentialResolver {
	return NewEnvCredentialResolver(
		CredentialBinding{StoreID: "store-1", Provider: ProviderTelegram, CredentialRef: "env://TELEGRAM_BOT_TOKEN_UNIT_TEST"},
		CredentialBinding{StoreID: "store-1", Provider: ProviderTelegram, CredentialRef: "env://TELEGRAM_BOT_TOKEN_DEFINITELY_UNSET_12345"},
		CredentialBinding{StoreID: "store-1", Provider: ProviderWhatsApp, ProviderAccountRef: fakeWhatsAppPhoneID, CredentialRef: "env://WHATSAPP_ACCESS_TOKEN_UNIT_TEST"},
	)
}

func TestCredentialScopeRejectsUnrelatedSecretsAndCrossStoreUse(t *testing.T) {
	t.Setenv("JWT_ACCESS_SECRET", "synthetic-secret-not-a-provider-token")
	t.Setenv("TELEGRAM_BOT_TOKEN_UNIT_TEST", fakeTelegramToken)
	resolver := newSenderTestResolver()
	valid := testSendRequest()
	if value, err := resolver.Resolve(context.Background(), valid); err != nil || value != fakeTelegramToken {
		t.Fatalf("valid binding rejected: %v", err)
	}
	cases := []SendRequest{valid, valid, valid, valid, valid}
	cases[0].CredentialRef = "env://JWT_ACCESS_SECRET"
	cases[1].StoreID = "other-store"
	cases[2].Provider = ProviderWhatsApp
	cases[3].ProviderAccountRef = "other-account"
	cases[4].StoreID = ""
	for _, req := range cases {
		if _, err := resolver.Resolve(context.Background(), req); err == nil {
			t.Fatal("unauthorized reference resolved")
		}
	}
	if _, err := NewEnvCredentialResolver().Resolve(context.Background(), valid); err == nil {
		t.Fatal("empty policy must deny")
	}
	// Even an incorrectly configured binding cannot grant a non-provider secret.
	bad := NewEnvCredentialResolver(CredentialBinding{StoreID: "store-1", Provider: ProviderTelegram, CredentialRef: "env://JWT_ACCESS_SECRET"})
	if _, err := bad.Resolve(context.Background(), cases[0]); err == nil {
		t.Fatal("unrelated secret resolved")
	}
}

func TestUnrelatedSecretCannotReachProvider(t *testing.T) {
	t.Setenv("JWT_ACCESS_SECRET", "synthetic-secret-not-a-provider-token")
	harness := newTelegramTestSender(t, func(http.ResponseWriter, *http.Request) { t.Error("unauthorized credential reached provider") })
	req := testSendRequest()
	req.CredentialRef = "env://JWT_ACCESS_SECRET"
	if _, err := harness.sender.Send(context.Background(), req); err == nil {
		t.Fatal("invalid credential accepted")
	}
	if harness.hits.Load() != 0 {
		t.Fatal("unexpected HTTP request")
	}
}

func TestCredentialBindingConfigurationFailsClosed(t *testing.T) {
	good := `[{"storeId":"11111111-1111-4111-8111-111111111111","provider":"TELEGRAM","providerAccountRef":"","credentialRef":"env://TELEGRAM_BOT_TOKEN"}]`
	if _, err := ParseCredentialBindings(good); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{good + " garbage", strings.ReplaceAll(good, "TELEGRAM_BOT_TOKEN", "JWT_ACCESS_SECRET"), strings.ReplaceAll(good, "11111111-1111-4111-8111-111111111111", "*"), `[{"extra":true}]`} {
		if _, err := ParseCredentialBindings(raw); err == nil {
			t.Fatal("invalid binding config accepted")
		}
	}
}

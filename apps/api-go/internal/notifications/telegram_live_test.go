package notifications

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTelegramLiveSmoke(t *testing.T) {
	if os.Getenv("RUN_TELEGRAM_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_TELEGRAM_INTEGRATION_TESTS=1 with TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID in the process environment to run the live Telegram smoke test")
	}
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	chatID := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	if token == "" || chatID == "" {
		t.Skip("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must be provided in the process environment; the smoke test did not run and no destination was guessed")
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", token)
	sender := NewTelegramSender(NewEnvCredentialResolver(), TelegramSenderOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := sender.Send(ctx, SendRequest{
		DeliveryID:      "live-smoke-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Provider:        ProviderTelegram,
		DestinationRef:  chatID,
		CredentialRef:   EnvCredentialScheme + "TELEGRAM_BOT_TOKEN",
		TemplateVersion: TelegramTemplateVersion,
		Payload:         BuildTestPayload(ProviderTelegram),
	})
	if err != nil {
		t.Fatalf("live telegram smoke send failed: %v", err)
	}
	if result.ProviderMessageID == "" {
		t.Fatal("live telegram smoke send returned no message id")
	}
}

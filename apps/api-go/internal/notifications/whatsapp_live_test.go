package notifications

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWhatsAppLiveSmoke(t *testing.T) {
	if os.Getenv("RUN_WHATSAPP_INTEGRATION_TESTS") != "1" {
		t.Skip("set RUN_WHATSAPP_INTEGRATION_TESTS=1 with the required WhatsApp variables in the process environment to run the live smoke test")
	}
	token := strings.TrimSpace(os.Getenv("WHATSAPP_ACCESS_TOKEN"))
	phoneNumberID := strings.TrimSpace(os.Getenv("WHATSAPP_PHONE_NUMBER_ID"))
	wabaID := strings.TrimSpace(os.Getenv("WHATSAPP_WABA_ID"))
	recipient := strings.TrimSpace(os.Getenv("WHATSAPP_RECIPIENT_PHONE"))
	testVideoURL := strings.TrimSpace(os.Getenv("WHATSAPP_TEST_VIDEO_URL"))
	if token == "" || phoneNumberID == "" || wabaID == "" || recipient == "" || testVideoURL == "" {
		t.Skip("WHATSAPP_ACCESS_TOKEN, WHATSAPP_PHONE_NUMBER_ID, WHATSAPP_WABA_ID, WHATSAPP_RECIPIENT_PHONE and WHATSAPP_TEST_VIDEO_URL are required; no provider request was made")
	}
	t.Setenv("WHATSAPP_ACCESS_TOKEN", token)
	config, err := json.Marshal(map[string]any{
		"wabaId":           wabaID,
		"templateName":     WhatsAppLinkedTemplateName,
		"templateLanguage": WhatsAppTemplateLanguage,
		"templateVersion":  WhatsAppLinkedTemplateVersion,
		"testVideoUrl":     testVideoURL,
		"optIn": map[string]any{
			"capturedAt":    time.Now().UTC().Format(time.RFC3339),
			"source":        "LIVE_SMOKE_TEST",
			"policyVersion": "whatsapp-emergency-alerts-v1",
		},
	})
	if err != nil {
		t.Fatal("could not build live smoke configuration")
	}
	bindings, bindingErr := ParseCredentialBindings(os.Getenv("NOTIFICATION_CREDENTIAL_BINDINGS"))
	if bindingErr != nil {
		t.Fatal(bindingErr)
	}
	storeID := strings.TrimSpace(os.Getenv("NOTIFICATION_TEST_STORE_ID"))
	if storeID == "" {
		t.Fatal("NOTIFICATION_TEST_STORE_ID is required for a scoped live smoke test")
	}
	sender := NewWhatsAppSender(NewEnvCredentialResolver(bindings...), WhatsAppSenderOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := sender.Send(ctx, SendRequest{StoreID: storeID,
		DeliveryID:         "live-smoke-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Provider:           ProviderWhatsApp,
		ProviderAccountRef: phoneNumberID,
		DestinationRef:     recipient,
		CredentialRef:      EnvCredentialScheme + "WHATSAPP_ACCESS_TOKEN",
		Config:             config,
		TemplateName:       WhatsAppLinkedTemplateName,
		TemplateLanguage:   WhatsAppTemplateLanguage,
		TemplateVersion:    WhatsAppLinkedTemplateVersion,
		Payload:            BuildTestPayload(ProviderWhatsApp),
	})
	if err != nil {
		t.Fatalf("live WhatsApp smoke send failed with code %v", err)
	}
	if strings.TrimSpace(result.ProviderMessageID) == "" {
		t.Fatal("live WhatsApp smoke send returned no provider message id")
	}
}

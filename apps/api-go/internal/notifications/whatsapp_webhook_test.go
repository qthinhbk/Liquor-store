package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestVerifyWhatsAppWebhookSignature(t *testing.T) {
	secret := "test-app-secret"
	body := []byte(`{"object":"whatsapp_business_account","entry":[]}`)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifyWhatsAppWebhookSignature(secret, body, signature) {
		t.Fatal("valid WhatsApp signature was rejected")
	}
	for _, invalid := range []struct {
		secret    string
		body      []byte
		signature string
	}{
		{"", body, signature},
		{secret, []byte(`{"changed":true}`), signature},
		{secret, body, "sha256=not-hex"},
		{secret, body, strings.Replace(signature, "a", "b", 1)},
		{secret, body, ""},
	} {
		if VerifyWhatsAppWebhookSignature(invalid.secret, invalid.body, invalid.signature) {
			t.Fatal("invalid WhatsApp signature was accepted")
		}
	}
}

func TestParseWhatsAppStatusEvents(t *testing.T) {
	body := []byte(`{
  "object":"whatsapp_business_account",
  "entry":[{"changes":[{"field":"messages","value":{"statuses":[
    {"id":"wamid.second","status":"delivered","timestamp":"1787911202"},
    {"id":"wamid.first","status":"sent","timestamp":"1787911201"},
    {"id":"wamid.failed","status":"failed","timestamp":"1787911203","errors":[{"code":131026,"title":"do not persist this provider text"}]},
    {"id":"wamid.ignored","status":"unknown","timestamp":"1787911204"},
    {"id":"bad id","status":"read","timestamp":"1787911205"}
  ]}}]}]
}`)
	events, err := ParseWhatsAppStatusEvents(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("parsed %d events, want 3", len(events))
	}
	if events[0].ProviderMessageID != "wamid.first" || events[0].Status != ProviderReceiptSent {
		t.Fatalf("events were not normalized and sorted: %+v", events)
	}
	if events[1].Status != ProviderReceiptDelivered {
		t.Fatalf("delivered status not normalized: %+v", events[1])
	}
	if events[2].Status != ProviderReceiptFailed || events[2].ErrorCode != "131026" {
		t.Fatalf("failed event did not keep only the stable error code: %+v", events[2])
	}
	if !events[0].EventAt.Equal(time.Unix(1787911201, 0).UTC()) {
		t.Fatalf("unexpected event time: %s", events[0].EventAt)
	}
}

func TestParseWhatsAppStatusEventsRejectsMalformedEnvelope(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"object":`),
		[]byte(`{"object":"page","entry":[]}`),
	} {
		if _, err := ParseWhatsAppStatusEvents(body); err == nil {
			t.Fatalf("invalid webhook envelope accepted: %s", body)
		}
	}
}

func TestApplyWhatsAppStatusEventsRejectsUnsafeDirectInputBeforeDatabaseAccess(t *testing.T) {
	now := time.Now().UTC()
	for _, event := range []WhatsAppStatusEvent{
		{ProviderMessageID: "bad id", Status: ProviderReceiptSent, EventAt: now},
		{ProviderMessageID: "wamid.valid", Status: "UNKNOWN", EventAt: now},
		{ProviderMessageID: "wamid.valid", Status: ProviderReceiptSent},
		{ProviderMessageID: "wamid.valid", Status: ProviderReceiptFailed, EventAt: now, ErrorCode: "not-numeric"},
	} {
		if _, err := ApplyWhatsAppStatusEvents(context.Background(), nil, []WhatsAppStatusEvent{event}); err == nil || strings.Contains(err.Error(), "database") {
			t.Fatalf("unsafe event was not rejected before database access: %+v, err=%v", event, err)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/liquor-store/security-api/internal/config"
	"github.com/liquor-store/security-api/internal/notifications"
)

const productionRuleName = "Emergency notifications"

func main() {
	if os.Getenv("APPLY_PRODUCTION_NOTIFICATION_CONFIG") != "1" {
		fatal(errors.New("refusing to change notification routing without APPLY_PRODUCTION_NOTIFICATION_CONFIG=1"))
	}
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	telegramToken := requiredEnv("TELEGRAM_BOT_TOKEN")
	telegramChatID := requiredEnv("TELEGRAM_CHAT_ID")
	whatsAppToken := requiredEnv("WHATSAPP_ACCESS_TOKEN")
	phoneNumberID := requiredEnv("WHATSAPP_PHONE_NUMBER_ID")
	wabaID := requiredEnv("WHATSAPP_WABA_ID")
	recipientPhone := requiredEnv("WHATSAPP_RECIPIENT_PHONE")
	optInCapturedAt := requiredEnv("WHATSAPP_OPT_IN_CAPTURED_AT")
	if telegramToken == "" || whatsAppToken == "" {
		fatal(errors.New("provider credentials are required"))
	}
	if _, err := time.Parse(time.RFC3339, optInCapturedAt); err != nil {
		fatal(errors.New("WHATSAPP_OPT_IN_CAPTURED_AT must be RFC3339"))
	}
	whatsAppConfig, err := json.Marshal(map[string]any{
		"wabaId": wabaID, "templateName": notifications.WhatsAppLinkedTemplateName,
		"templateLanguage": notifications.WhatsAppTemplateLanguage, "templateVersion": notifications.WhatsAppLinkedTemplateVersion,
		"optIn": map[string]any{"capturedAt": optInCapturedAt, "source": "OWNER_PRODUCTION_CONFIGURATION", "policyVersion": "whatsapp-emergency-alerts-v1"},
	})
	if err != nil {
		fatal(err)
	}
	if err := notifications.ValidateWhatsAppEnableConfig(phoneNumberID, recipientPhone, whatsAppConfig); err != nil {
		fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, cfg.MigrationURL())
	if err != nil {
		fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	storeID, err := onlyStore(ctx, tx)
	if err != nil {
		fatal(err)
	}
	telegramConfig := json.RawMessage(`{"parseMode":"HTML"}`)
	telegramID, err := configureEndpoint(ctx, tx, storeID, string(notifications.ProviderTelegram), "Owner Telegram", "", telegramChatID, "env://TELEGRAM_BOT_TOKEN", telegramConfig)
	if err != nil {
		fatal(err)
	}
	whatsAppID, err := configureEndpoint(ctx, tx, storeID, string(notifications.ProviderWhatsApp), "Owner WhatsApp", phoneNumberID, recipientPhone, "env://WHATSAPP_ACCESS_TOKEN", whatsAppConfig)
	if err != nil {
		fatal(err)
	}
	ruleID, err := configureRule(ctx, tx, storeID)
	if err != nil {
		fatal(err)
	}
	if err := configureChannel(ctx, tx, storeID, ruleID, telegramID, 1, 0); err != nil {
		fatal(err)
	}
	if err := configureChannel(ctx, tx, storeID, ruleID, whatsAppID, 2, 60); err != nil {
		fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		fatal(err)
	}
	fmt.Println("Configured one emergency rule with Telegram primary and WhatsApp fallback; no credentials or destinations were printed.")
}

func onlyStore(ctx context.Context, tx pgx.Tx) (string, error) {
	rows, err := tx.Query(ctx, `SELECT "id" FROM "stores" ORDER BY "createdAt","id" LIMIT 2`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("expected exactly one production store, found %d; set up routing through the owner API instead", len(ids))
	}
	return ids[0], nil
}

func configureEndpoint(ctx context.Context, tx pgx.Tx, storeID, provider, label, providerAccountRef, destinationRef, credentialRef string, endpointConfig json.RawMessage) (string, error) {
	rows, err := tx.Query(ctx, `SELECT "id" FROM "notification_endpoints" WHERE "storeId"=$1 AND "provider"=$2 ORDER BY "createdAt","id" LIMIT 2`, storeID, provider)
	if err != nil {
		return "", err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	if len(ids) > 1 {
		return "", fmt.Errorf("multiple %s endpoints exist; refusing to guess which one to update", provider)
	}
	accountRef := any(nil)
	if strings.TrimSpace(providerAccountRef) != "" {
		accountRef = providerAccountRef
	}
	if len(ids) == 1 {
		_, err = tx.Exec(ctx, `UPDATE "notification_endpoints" SET "label"=$3,"providerAccountRef"=$4,"destinationRef"=$5,"credentialRef"=$6,"config"=$7,"isEnabled"=true,"updatedAt"=NOW() WHERE "id"=$1 AND "storeId"=$2`, ids[0], storeID, label, accountRef, destinationRef, credentialRef, endpointConfig)
		return ids[0], err
	}
	id := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO "notification_endpoints" ("id","storeId","provider","label","providerAccountRef","destinationRef","credentialRef","config","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true,NOW())`, id, storeID, provider, label, accountRef, destinationRef, credentialRef, endpointConfig)
	return id, err
}

func configureRule(ctx context.Context, tx pgx.Tx, storeID string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT "id" FROM "notification_rules" WHERE "storeId"=$1 AND "name"=$2 ORDER BY "createdAt","id" LIMIT 1`, storeID, productionRuleName).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		id = uuid.NewString()
		_, err = tx.Exec(ctx, `INSERT INTO "notification_rules" ("id","storeId","name","minimumSeverity","alertTypes","cooldownSeconds","isEnabled","updatedAt") VALUES ($1,$2,$3,'CRITICAL',ARRAY['WEAPON_DETECTED']::"AlertType"[],0,true,NOW())`, id, storeID, productionRuleName)
		return id, err
	}
	_, err = tx.Exec(ctx, `UPDATE "notification_rules" SET "minimumSeverity"='CRITICAL',"alertTypes"=ARRAY['WEAPON_DETECTED']::"AlertType"[],"cooldownSeconds"=0,"isEnabled"=true,"updatedAt"=NOW() WHERE "id"=$1 AND "storeId"=$2`, id, storeID)
	return id, err
}

func configureChannel(ctx context.Context, tx pgx.Tx, storeID, ruleID, endpointID string, priority, fallbackDelay int) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT "id" FROM "notification_rule_channels" WHERE "ruleId"=$1 AND "endpointId"=$2`, ruleID, endpointID).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO "notification_rule_channels" ("id","storeId","ruleId","endpointId","priority","fallbackDelaySeconds","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,true,NOW())`, uuid.NewString(), storeID, ruleID, endpointID, priority, fallbackDelay)
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE "notification_rule_channels" SET "priority"=$3,"fallbackDelaySeconds"=$4,"isEnabled"=true,"updatedAt"=NOW() WHERE "id"=$1 AND "storeId"=$2`, id, storeID, priority, fallbackDelay)
	return err
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		fatal(fmt.Errorf("%s is required", name))
	}
	return value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

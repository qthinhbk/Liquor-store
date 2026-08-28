CREATE TYPE "NotificationProvider" AS ENUM ('TELEGRAM', 'WHATSAPP');
CREATE TYPE "NotificationDeliveryKind" AS ENUM ('ALERT', 'TEST');
CREATE TYPE "NotificationDeliveryStatus" AS ENUM ('WAITING_FALLBACK', 'PENDING', 'PROCESSING', 'RETRY_SCHEDULED', 'SENT', 'FAILED', 'CANCELLED');
CREATE TYPE "NotificationAttemptStatus" AS ENUM ('SUCCEEDED', 'FAILED');

CREATE TABLE "notification_endpoints" (
  "id" UUID PRIMARY KEY,
  "storeId" UUID NOT NULL,
  "provider" "NotificationProvider" NOT NULL,
  "label" TEXT NOT NULL,
  "providerAccountRef" TEXT,
  "destinationRef" TEXT NOT NULL,
  "credentialRef" TEXT NOT NULL,
  "config" JSONB NOT NULL DEFAULT '{}'::jsonb,
  "isEnabled" BOOLEAN NOT NULL DEFAULT true,
  "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updatedAt" TIMESTAMP(3) NOT NULL,
  CONSTRAINT "notification_endpoints_config_object_check" CHECK (jsonb_typeof("config") = 'object')
);

CREATE TABLE "notification_rules" (
  "id" UUID PRIMARY KEY,
  "storeId" UUID NOT NULL,
  "name" TEXT NOT NULL,
  "minimumSeverity" "AlertSeverity" NOT NULL DEFAULT 'CRITICAL',
  "alertTypes" "AlertType"[] NOT NULL DEFAULT ARRAY[]::"AlertType"[],
  "cooldownSeconds" INTEGER NOT NULL DEFAULT 0,
  "isEnabled" BOOLEAN NOT NULL DEFAULT true,
  "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updatedAt" TIMESTAMP(3) NOT NULL,
  CONSTRAINT "notification_rules_cooldown_check" CHECK ("cooldownSeconds" BETWEEN 0 AND 86400)
);

CREATE TABLE "notification_rule_channels" (
  "id" UUID PRIMARY KEY,
  "storeId" UUID NOT NULL,
  "ruleId" UUID NOT NULL,
  "endpointId" UUID NOT NULL,
  "priority" SMALLINT NOT NULL DEFAULT 1,
  "fallbackDelaySeconds" INTEGER NOT NULL DEFAULT 0,
  "isEnabled" BOOLEAN NOT NULL DEFAULT true,
  "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updatedAt" TIMESTAMP(3) NOT NULL,
  CONSTRAINT "notification_rule_channels_priority_check" CHECK ("priority" >= 1),
  CONSTRAINT "notification_rule_channels_fallback_delay_check" CHECK ("fallbackDelaySeconds" BETWEEN 0 AND 86400)
);

CREATE TABLE "notification_deliveries" (
  "id" UUID PRIMARY KEY,
  "deliveryKind" "NotificationDeliveryKind" NOT NULL DEFAULT 'ALERT',
  "storeId" UUID NOT NULL,
  "alertId" UUID,
  "ruleId" UUID,
  "endpointId" UUID NOT NULL,
  "ruleChannelId" UUID,
  "dedupeKey" TEXT NOT NULL,
  "status" "NotificationDeliveryStatus" NOT NULL DEFAULT 'PENDING',
  "provider" "NotificationProvider" NOT NULL,
  "priority" SMALLINT NOT NULL DEFAULT 1,
  "fallbackDelaySeconds" INTEGER NOT NULL DEFAULT 0,
  "templateVersion" TEXT NOT NULL,
  "payload" JSONB NOT NULL,
  "providerMessageId" TEXT,
  "attemptCount" INTEGER NOT NULL DEFAULT 0,
  "maxAttempts" INTEGER NOT NULL DEFAULT 5,
  "availableAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "lockedAt" TIMESTAMP(3),
  "lockedUntil" TIMESTAMP(3),
  "lastAttemptAt" TIMESTAMP(3),
  "sentAt" TIMESTAMP(3),
  "lastErrorCode" TEXT,
  "lastErrorMessage" TEXT,
  "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updatedAt" TIMESTAMP(3) NOT NULL,
  CONSTRAINT "notification_deliveries_payload_object_check" CHECK (jsonb_typeof("payload") = 'object'),
  CONSTRAINT "notification_deliveries_attempt_count_check" CHECK ("attemptCount" >= 0),
  CONSTRAINT "notification_deliveries_max_attempts_check" CHECK ("maxAttempts" BETWEEN 1 AND 20),
  CONSTRAINT "notification_deliveries_attempt_bounds_check" CHECK ("attemptCount" <= "maxAttempts"),
  CONSTRAINT "notification_deliveries_template_version_check" CHECK (char_length(btrim("templateVersion")) > 0),
  CONSTRAINT "notification_deliveries_kind_links_check" CHECK (
    ("deliveryKind" = 'ALERT' AND "alertId" IS NOT NULL AND "ruleId" IS NOT NULL AND "ruleChannelId" IS NOT NULL)
    OR ("deliveryKind" = 'TEST' AND "alertId" IS NULL AND "ruleId" IS NULL AND "ruleChannelId" IS NULL)
  ),
  CONSTRAINT "notification_deliveries_lease_status_check" CHECK (
    ("status" = 'PROCESSING' AND "lockedAt" IS NOT NULL AND "lockedUntil" IS NOT NULL AND "lockedUntil" > "lockedAt")
    OR ("status" <> 'PROCESSING' AND "lockedAt" IS NULL AND "lockedUntil" IS NULL)
  )
);

CREATE TABLE "notification_attempts" (
  "id" UUID PRIMARY KEY,
  "deliveryId" UUID NOT NULL,
  "attemptNumber" INTEGER NOT NULL,
  "status" "NotificationAttemptStatus" NOT NULL,
  "startedAt" TIMESTAMP(3) NOT NULL,
  "finishedAt" TIMESTAMP(3) NOT NULL,
  "durationMs" INTEGER NOT NULL,
  "responseStatus" INTEGER,
  "providerMessageId" TEXT,
  "errorCode" TEXT,
  "errorMessage" TEXT,
  "responseMetadata" JSONB NOT NULL DEFAULT '{}'::jsonb,
  "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT "notification_attempts_attempt_number_check" CHECK ("attemptNumber" >= 1),
  CONSTRAINT "notification_attempts_duration_check" CHECK ("durationMs" >= 0),
  CONSTRAINT "notification_attempts_response_metadata_object_check" CHECK (jsonb_typeof("responseMetadata") = 'object')
);

-- Composite unique keys let child tables pin a parent row to one store, so a
-- delivery can never reference an alert, rule, endpoint or channel from another store.
CREATE UNIQUE INDEX "alerts_id_store_key" ON "alerts"("id", "storeId");
CREATE UNIQUE INDEX "notification_endpoints_id_store_key" ON "notification_endpoints"("id", "storeId");
CREATE UNIQUE INDEX "notification_rules_id_store_key" ON "notification_rules"("id", "storeId");
CREATE UNIQUE INDEX "notification_rule_channels_id_store_key" ON "notification_rule_channels"("id", "storeId");
CREATE UNIQUE INDEX "notification_rule_channels_id_route_key" ON "notification_rule_channels"("id", "ruleId", "endpointId", "storeId");

CREATE UNIQUE INDEX "notification_endpoints_store_provider_destination_key" ON "notification_endpoints"("storeId", "provider", "destinationRef");
CREATE INDEX "notification_endpoints_store_provider_enabled_idx" ON "notification_endpoints"("storeId", "provider", "isEnabled");
CREATE INDEX "notification_rules_store_enabled_severity_idx" ON "notification_rules"("storeId", "isEnabled", "minimumSeverity");
CREATE UNIQUE INDEX "notification_rule_channels_rule_endpoint_key" ON "notification_rule_channels"("ruleId", "endpointId");
CREATE UNIQUE INDEX "notification_rule_channels_rule_priority_key" ON "notification_rule_channels"("ruleId", "priority");
CREATE UNIQUE INDEX "notification_deliveries_dedupe_key" ON "notification_deliveries"("dedupeKey");
CREATE UNIQUE INDEX "notification_deliveries_alert_rule_endpoint_key" ON "notification_deliveries"("alertId", "ruleId", "endpointId");
CREATE INDEX "notification_deliveries_status_available_idx" ON "notification_deliveries"("status", "availableAt");
CREATE INDEX "notification_deliveries_lease_recovery_idx" ON "notification_deliveries"("lockedUntil") WHERE "status" = 'PROCESSING';
CREATE INDEX "notification_deliveries_alert_idx" ON "notification_deliveries"("alertId");
CREATE INDEX "notification_deliveries_store_created_idx" ON "notification_deliveries"("storeId", "createdAt");
CREATE UNIQUE INDEX "notification_attempts_delivery_number_key" ON "notification_attempts"("deliveryId", "attemptNumber");
CREATE INDEX "notification_attempts_delivery_created_idx" ON "notification_attempts"("deliveryId", "createdAt");

ALTER TABLE "notification_endpoints" ADD CONSTRAINT "notification_endpoints_store_fkey" FOREIGN KEY ("storeId") REFERENCES "stores"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "notification_rules" ADD CONSTRAINT "notification_rules_store_fkey" FOREIGN KEY ("storeId") REFERENCES "stores"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "notification_rule_channels" ADD CONSTRAINT "notification_rule_channels_rule_fkey" FOREIGN KEY ("ruleId", "storeId") REFERENCES "notification_rules"("id", "storeId") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "notification_rule_channels" ADD CONSTRAINT "notification_rule_channels_endpoint_fkey" FOREIGN KEY ("endpointId", "storeId") REFERENCES "notification_endpoints"("id", "storeId") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "notification_deliveries" ADD CONSTRAINT "notification_deliveries_alert_fkey" FOREIGN KEY ("alertId", "storeId") REFERENCES "alerts"("id", "storeId") ON DELETE RESTRICT ON UPDATE CASCADE;
ALTER TABLE "notification_deliveries" ADD CONSTRAINT "notification_deliveries_rule_fkey" FOREIGN KEY ("ruleId", "storeId") REFERENCES "notification_rules"("id", "storeId") ON DELETE RESTRICT ON UPDATE CASCADE;
ALTER TABLE "notification_deliveries" ADD CONSTRAINT "notification_deliveries_endpoint_fkey" FOREIGN KEY ("endpointId", "storeId") REFERENCES "notification_endpoints"("id", "storeId") ON DELETE RESTRICT ON UPDATE CASCADE;
ALTER TABLE "notification_deliveries" ADD CONSTRAINT "notification_deliveries_rule_channel_fkey" FOREIGN KEY ("ruleChannelId", "ruleId", "endpointId", "storeId") REFERENCES "notification_rule_channels"("id", "ruleId", "endpointId", "storeId") ON DELETE RESTRICT ON UPDATE CASCADE;
ALTER TABLE "notification_attempts" ADD CONSTRAINT "notification_attempts_delivery_fkey" FOREIGN KEY ("deliveryId") REFERENCES "notification_deliveries"("id") ON DELETE CASCADE ON UPDATE CASCADE;

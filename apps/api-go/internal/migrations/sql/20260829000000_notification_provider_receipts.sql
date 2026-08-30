CREATE TYPE "NotificationProviderReceiptStatus" AS ENUM ('SENT', 'DELIVERED', 'READ', 'FAILED');

ALTER TABLE "notification_deliveries"
  ADD COLUMN "providerStatus" "NotificationProviderReceiptStatus",
  ADD COLUMN "providerStatusAt" TIMESTAMPTZ(3),
  ADD COLUMN "deliveredAt" TIMESTAMPTZ(3),
  ADD COLUMN "readAt" TIMESTAMPTZ(3),
  ADD COLUMN "providerFailedAt" TIMESTAMPTZ(3),
  ADD COLUMN "providerErrorCode" TEXT;

CREATE UNIQUE INDEX "notification_deliveries_provider_message_key"
  ON "notification_deliveries"("provider", "providerMessageId")
  WHERE "providerMessageId" IS NOT NULL;

CREATE TABLE "notification_provider_events" (
  "id" UUID PRIMARY KEY,
  "deliveryId" UUID NOT NULL,
  "provider" "NotificationProvider" NOT NULL,
  "providerMessageId" TEXT NOT NULL,
  "status" "NotificationProviderReceiptStatus" NOT NULL,
  "eventAt" TIMESTAMPTZ(3) NOT NULL,
  "errorCode" TEXT,
  "receivedAt" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT "notification_provider_events_message_id_check"
    CHECK (char_length(btrim("providerMessageId")) BETWEEN 1 AND 256),
  CONSTRAINT "notification_provider_events_error_code_check"
    CHECK ("errorCode" IS NULL OR char_length("errorCode") BETWEEN 1 AND 120)
);

CREATE UNIQUE INDEX "notification_provider_events_dedupe_key"
  ON "notification_provider_events"("provider", "providerMessageId", "status", "eventAt");
CREATE INDEX "notification_provider_events_delivery_received_idx"
  ON "notification_provider_events"("deliveryId", "receivedAt");

ALTER TABLE "notification_provider_events"
  ADD CONSTRAINT "notification_provider_events_delivery_fkey"
  FOREIGN KEY ("deliveryId")
  REFERENCES "notification_deliveries"("id")
  ON DELETE CASCADE ON UPDATE CASCADE;

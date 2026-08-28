-- Runtime support for short-lived, evidence-scoped notification review links.
-- Only a SHA-256 token digest is stored; the bearer token itself never enters
-- PostgreSQL, logs, delivery payloads, or notification attempt metadata.

CREATE UNIQUE INDEX "alert_evidence_id_alert_key"
  ON "alert_evidence"("id", "alertId");

CREATE TABLE "notification_video_links" (
  "id" UUID PRIMARY KEY,
  "tokenHash" BYTEA NOT NULL UNIQUE,
  "storeId" UUID NOT NULL,
  "alertId" UUID NOT NULL,
  "evidenceId" UUID NOT NULL,
  "deliveryId" UUID,
  "expiresAt" TIMESTAMPTZ(3) NOT NULL,
  "revokedAt" TIMESTAMPTZ(3),
  "accessCount" INTEGER NOT NULL DEFAULT 0,
  "lastAccessedAt" TIMESTAMPTZ(3),
  "createdAt" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT "notification_video_links_expiry_check" CHECK ("expiresAt" > "createdAt"),
  CONSTRAINT "notification_video_links_access_count_check" CHECK ("accessCount" >= 0)
);

CREATE INDEX "notification_video_links_expiry_idx"
  ON "notification_video_links"("expiresAt")
  WHERE "revokedAt" IS NULL;
CREATE INDEX "notification_video_links_delivery_idx"
  ON "notification_video_links"("deliveryId");
CREATE INDEX "notification_video_links_alert_idx"
  ON "notification_video_links"("alertId", "createdAt");

ALTER TABLE "notification_video_links"
  ADD CONSTRAINT "notification_video_links_alert_fkey"
  FOREIGN KEY ("alertId", "storeId")
  REFERENCES "alerts"("id", "storeId")
  ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE "notification_video_links"
  ADD CONSTRAINT "notification_video_links_evidence_fkey"
  FOREIGN KEY ("evidenceId", "alertId")
  REFERENCES "alert_evidence"("id", "alertId")
  ON DELETE CASCADE ON UPDATE CASCADE;

ALTER TABLE "notification_video_links"
  ADD CONSTRAINT "notification_video_links_delivery_fkey"
  FOREIGN KEY ("deliveryId")
  REFERENCES "notification_deliveries"("id")
  ON DELETE CASCADE ON UPDATE CASCADE;

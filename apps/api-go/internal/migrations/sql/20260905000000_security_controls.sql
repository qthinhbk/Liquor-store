-- Keep immutable decision snapshots independently of resource deletion.
-- These UUIDs deliberately have no cascading foreign keys: removing an alert,
-- membership or account must not erase its decision history.
CREATE TABLE "alert_status_history" (
  "id" UUID PRIMARY KEY,
  "storeId" UUID NOT NULL,
  "alertId" UUID NOT NULL,
  "actorId" UUID NOT NULL,
  "actorRole" TEXT NOT NULL,
  "previousStatus" "AlertStatus" NOT NULL,
  "newStatus" "AlertStatus" NOT NULL,
  "previousActorId" UUID,
  "previousAcknowledgedAt" TIMESTAMPTZ,
  "previousNote" TEXT,
  "note" TEXT,
  "createdAt" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX "alert_status_history_alert_idx" ON "alert_status_history" ("storeId", "alertId", "createdAt");
CREATE FUNCTION reject_alert_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Alert status history is append-only' USING ERRCODE = '42501';
END;
$$;
CREATE TRIGGER "alert_status_history_immutable" BEFORE UPDATE OR DELETE ON "alert_status_history"
FOR EACH ROW EXECUTE FUNCTION reject_alert_history_mutation();
CREATE TRIGGER "alert_status_history_no_truncate" BEFORE TRUNCATE ON "alert_status_history"
FOR EACH STATEMENT EXECUTE FUNCTION reject_alert_history_mutation();

ALTER TABLE "notification_deliveries" ADD COLUMN "requestedById" UUID;
CREATE INDEX "notification_test_store_quota_idx" ON "notification_deliveries" ("storeId", "createdAt") WHERE "deliveryKind"='TEST';
CREATE INDEX "notification_test_user_quota_idx" ON "notification_deliveries" ("requestedById", "createdAt") WHERE "deliveryKind"='TEST';
CREATE INDEX "notification_test_recent_idx" ON "notification_deliveries" ("createdAt") WHERE "deliveryKind"='TEST';
CREATE INDEX "notification_test_pending_idx" ON "notification_deliveries" ("storeId", "endpointId") WHERE "deliveryKind"='TEST' AND "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED');
CREATE INDEX "notification_priority_ready_idx" ON "notification_deliveries" ((CASE WHEN "deliveryKind"='ALERT' THEN 0 ELSE 1 END), "availableAt", "createdAt", "id") WHERE "status" IN ('PENDING','RETRY_SCHEDULED');

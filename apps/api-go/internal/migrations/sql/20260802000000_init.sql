CREATE EXTENSION IF NOT EXISTS citext;

CREATE TYPE "UserStatus" AS ENUM ('ACTIVE', 'SUSPENDED');
CREATE TYPE "StoreMemberRole" AS ENUM ('OWNER', 'MANAGER', 'OPERATOR');
CREATE TYPE "CameraProtocol" AS ENUM ('RTSP', 'ONVIF', 'HLS', 'WEBRTC');
CREATE TYPE "CameraStatus" AS ENUM ('ONLINE', 'OFFLINE', 'DISABLED');
CREATE TYPE "ZoneKind" AS ENUM ('HIGH_VALUE', 'CASHIER', 'STOCKROOM', 'ENTRANCE', 'CUSTOM');
CREATE TYPE "PersonCategory" AS ENUM ('EMPLOYEE', 'CUSTOMER', 'UNKNOWN');
CREATE TYPE "AlertType" AS ENUM ('CASH_DRAWER_WITHOUT_CUSTOMER', 'SUSPICIOUS_CASH_HANDLING', 'POS_VOID_OR_REFUND', 'UNAUTHORIZED_STOCKROOM_ACCESS', 'HIGH_VALUE_ZONE_DWELL', 'WEAPON_DETECTED', 'SUSPICIOUS_PRODUCT_CONCEALMENT');
CREATE TYPE "AlertSeverity" AS ENUM ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL');
CREATE TYPE "AlertStatus" AS ENUM ('NEW', 'ACKNOWLEDGED', 'DISMISSED', 'RESOLVED');

CREATE TABLE "users" ("id" UUID PRIMARY KEY, "email" CITEXT NOT NULL UNIQUE, "passwordHash" TEXT NOT NULL, "displayName" TEXT NOT NULL, "status" "UserStatus" NOT NULL DEFAULT 'ACTIVE', "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);
CREATE TABLE "stores" ("id" UUID PRIMARY KEY, "name" TEXT NOT NULL, "code" TEXT NOT NULL UNIQUE, "address" TEXT, "timezone" TEXT NOT NULL DEFAULT 'Asia/Ho_Chi_Minh', "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);
CREATE TABLE "store_memberships" ("id" UUID PRIMARY KEY, "userId" UUID NOT NULL, "storeId" UUID NOT NULL, "role" "StoreMemberRole" NOT NULL, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);
CREATE TABLE "cameras" ("id" UUID PRIMARY KEY, "storeId" UUID NOT NULL, "name" TEXT NOT NULL, "location" TEXT NOT NULL, "protocol" "CameraProtocol" NOT NULL, "streamGatewayRef" TEXT NOT NULL UNIQUE, "status" "CameraStatus" NOT NULL DEFAULT 'OFFLINE', "isEnabled" BOOLEAN NOT NULL DEFAULT true, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);
CREATE TABLE "camera_zones" ("id" UUID PRIMARY KEY, "cameraId" UUID NOT NULL, "name" TEXT NOT NULL, "kind" "ZoneKind" NOT NULL, "expectedPersonCategory" "PersonCategory", "polygon" JSONB NOT NULL, "dwellThresholdSeconds" INTEGER, "isEnabled" BOOLEAN NOT NULL DEFAULT true, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);
CREATE TABLE "alerts" ("id" UUID PRIMARY KEY, "sourceEventId" TEXT UNIQUE, "correlationId" TEXT, "storeId" UUID NOT NULL, "cameraId" UUID, "zoneId" UUID, "type" "AlertType" NOT NULL, "severity" "AlertSeverity" NOT NULL, "status" "AlertStatus" NOT NULL DEFAULT 'NEW', "subjectPersonCategory" "PersonCategory" NOT NULL DEFAULT 'UNKNOWN', "confidence" DECIMAL(5,4), "detectedAt" TIMESTAMP(3) NOT NULL, "observedStartAt" TIMESTAMP(3), "observedEndAt" TIMESTAMP(3), "acknowledgedAt" TIMESTAMP(3), "acknowledgedById" UUID, "resolutionNote" TEXT, "metadata" JSONB, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);
CREATE TABLE "alert_evidence" ("id" UUID PRIMARY KEY, "alertId" UUID NOT NULL, "storageKey" TEXT NOT NULL UNIQUE, "mimeType" TEXT NOT NULL, "durationSeconds" INTEGER NOT NULL, "startsAt" TIMESTAMP(3) NOT NULL, "endsAt" TIMESTAMP(3) NOT NULL, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE "refresh_sessions" ("id" UUID PRIMARY KEY, "userId" UUID NOT NULL, "tokenHash" TEXT NOT NULL UNIQUE, "expiresAt" TIMESTAMP(3) NOT NULL, "revokedAt" TIMESTAMP(3), "ipAddress" TEXT, "userAgent" TEXT, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL);

CREATE UNIQUE INDEX "store_memberships_userId_storeId_key" ON "store_memberships"("userId", "storeId");
CREATE INDEX "store_memberships_storeId_role_idx" ON "store_memberships"("storeId", "role");
CREATE INDEX "cameras_storeId_status_idx" ON "cameras"("storeId", "status");
CREATE INDEX "camera_zones_cameraId_kind_idx" ON "camera_zones"("cameraId", "kind");
CREATE INDEX "alerts_storeId_status_detectedAt_idx" ON "alerts"("storeId", "status", "detectedAt");
CREATE INDEX "alerts_storeId_correlationId_idx" ON "alerts"("storeId", "correlationId");
CREATE INDEX "alerts_cameraId_detectedAt_idx" ON "alerts"("cameraId", "detectedAt");
CREATE INDEX "alerts_type_severity_detectedAt_idx" ON "alerts"("type", "severity", "detectedAt");
CREATE INDEX "alert_evidence_alertId_idx" ON "alert_evidence"("alertId");
CREATE INDEX "refresh_sessions_userId_expiresAt_idx" ON "refresh_sessions"("userId", "expiresAt");

ALTER TABLE "store_memberships" ADD CONSTRAINT "store_memberships_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "store_memberships" ADD CONSTRAINT "store_memberships_storeId_fkey" FOREIGN KEY ("storeId") REFERENCES "stores"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "cameras" ADD CONSTRAINT "cameras_storeId_fkey" FOREIGN KEY ("storeId") REFERENCES "stores"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "camera_zones" ADD CONSTRAINT "camera_zones_cameraId_fkey" FOREIGN KEY ("cameraId") REFERENCES "cameras"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "alerts" ADD CONSTRAINT "alerts_storeId_fkey" FOREIGN KEY ("storeId") REFERENCES "stores"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "alerts" ADD CONSTRAINT "alerts_cameraId_fkey" FOREIGN KEY ("cameraId") REFERENCES "cameras"("id") ON DELETE SET NULL ON UPDATE CASCADE;
ALTER TABLE "alerts" ADD CONSTRAINT "alerts_zoneId_fkey" FOREIGN KEY ("zoneId") REFERENCES "camera_zones"("id") ON DELETE SET NULL ON UPDATE CASCADE;
ALTER TABLE "alerts" ADD CONSTRAINT "alerts_acknowledgedById_fkey" FOREIGN KEY ("acknowledgedById") REFERENCES "users"("id") ON DELETE SET NULL ON UPDATE CASCADE;
ALTER TABLE "alert_evidence" ADD CONSTRAINT "alert_evidence_alertId_fkey" FOREIGN KEY ("alertId") REFERENCES "alerts"("id") ON DELETE CASCADE ON UPDATE CASCADE;
ALTER TABLE "refresh_sessions" ADD CONSTRAINT "refresh_sessions_userId_fkey" FOREIGN KEY ("userId") REFERENCES "users"("id") ON DELETE CASCADE ON UPDATE CASCADE;

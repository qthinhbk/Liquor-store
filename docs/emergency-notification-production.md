# Emergency Notification API production runbook

## Acceptance criteria

Task 6 is complete only when one controlled alert passes this entire chain:

1. `POST /api/v1/internal/ai/alerts` authenticates the AI service and creates a UUID alert.
2. The alert, evidence and notification deliveries commit in one PostgreSQL transaction.
3. The worker records an immutable attempt and sends through the configured primary route.
4. Unused fallback routes finish as `CANCELLED`; a failed primary activates the next route.
5. The dashboard retains the evidence-backed alert, requests a short-lived playback URL and opens the exact `alertId` from the WhatsApp `View alert` button.
6. WhatsApp receipts are accepted only through an HMAC-verified webhook and remain visible in the delivery audit.

The controlled event must never contact police or any emergency service.

## Render environment

Set secret values in Render, never in Git:

- `AI_INGEST_TOKEN` — random opaque value, at least 32 characters.
- `TELEGRAM_BOT_TOKEN`
- `WHATSAPP_ACCESS_TOKEN`
- `WHATSAPP_WEBHOOK_VERIFY_TOKEN` — a separate random value used during Meta webhook verification.
- `WHATSAPP_APP_SECRET` — the Meta app secret, not the WhatsApp access token.

Set these runtime values:

- `NODE_ENV=production`
- `NOTIFICATION_WORKER_ENABLED=true`
- `NOTIFICATION_POLL_INTERVAL=2s`
- `NOTIFICATION_LEASE_DURATION=45s`
- `NOTIFICATION_BATCH_SIZE=10`
- `PUBLIC_API_BASE_URL=https://liquor-store-api-7tq2.onrender.com`
- `EVIDENCE_ORIGIN_BASE_URL=https://ketchenterprise.net`
- `SECURE_VIDEO_LINK_TTL=15m`

Keep the existing production `DATABASE_URL`, `DIRECT_URL`, JWT and origin settings unchanged.

## Meta webhook

Use this callback in the Meta WhatsApp configuration:

`https://liquor-store-api-7tq2.onrender.com/api/v1/webhooks/whatsapp`

The verify token must exactly match `WHATSAPP_WEBHOOK_VERIFY_TOKEN`. Subscribe the WhatsApp Business Account to the `messages` field after verification.

## Deployment and verification

```powershell
Set-Location 'D:\Liquor-store\apps\api-go'
go run ./cmd/migrate
$env:RUN_PRODUCTION_READINESS='1'
go test -run '^TestProductionNotificationDatabaseReadiness$' -count=1 -v ./internal/server
```

After the Render deployment is healthy and the worker is enabled, place the same `AI_INGEST_TOKEN` in the local untracked `.env`, then run exactly one controlled alert:

```powershell
$env:RUN_PRODUCTION_E2E='1'
go test -run '^TestProductionEmergencyNotificationE2E$' -count=1 -v ./internal/server
```

The test prints no alert, store, camera, destination or provider identifiers. It leaves the alert and attempt records in PostgreSQL as production audit evidence.

## Rollback

1. Set `NOTIFICATION_WORKER_ENABLED=false` to stop new sends without deleting audit history.
2. Keep the ingestion endpoint deployed; unset `AI_INGEST_TOKEN` only if AI ingestion itself must be disabled.
3. Roll Render back to the previous healthy deployment if the API fails its health check.
4. Do not delete notification deliveries, attempts or provider events. They are the incident audit trail.

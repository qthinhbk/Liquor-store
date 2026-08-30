# REST API contract

All application endpoints use the `/api/v1` prefix, JSON bodies and ISO-8601 timestamps. Authenticated endpoints require a bearer access token. Responses use `401` for unauthenticated requests, `403` for insufficient store role and `404` when the requested resource is outside the caller's store scope.

## Authentication

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/auth/register` | Create the first user, store and owner membership atomically. |
| POST | `/auth/login` | Create a session with email and password. |
| POST | `/auth/refresh` | Rotate the refresh session and return a new access token. |
| POST | `/auth/logout` | Revoke the active refresh session. |
| GET | `/auth/me` | Return the caller and accessible stores. |

## Stores and members

| Method | Path | Minimum role |
| --- | --- | --- |
| GET | `/stores` | authenticated |
| POST | `/stores` | platform bootstrap / owner |
| GET, PATCH | `/stores/:storeId` | store member / owner |
| GET, POST | `/stores/:storeId/members` | manager / owner |
| PATCH, DELETE | `/stores/:storeId/members/:userId` | owner |

## Cameras and zones

| Method | Path | Minimum role |
| --- | --- | --- |
| GET | `/stores/:storeId/cameras` | operator |
| POST | `/stores/:storeId/cameras` | manager |
| GET | `/stores/:storeId/cameras/:cameraId` | operator |
| PATCH, DELETE | `/stores/:storeId/cameras/:cameraId` | manager |
| GET | `/stores/:storeId/cameras/:cameraId/zones` | operator |
| POST | `/stores/:storeId/cameras/:cameraId/zones` | manager |
| PATCH, DELETE | `/stores/:storeId/cameras/:cameraId/zones/:zoneId` | manager |

`POST`/`PATCH` camera payloads contain only metadata and `streamGatewayRef`, never camera credentials. A zone payload has `name`, `kind`, `polygon`, optional `dwellThresholdSeconds` and optional `expectedPersonCategory`.

For a cashier camera, configuration must create an inner-counter zone with `expectedPersonCategory=EMPLOYEE` and an outer-counter zone with `expectedPersonCategory=CUSTOMER`. The classification is based on the detected person's position in the image, not identity recognition.

Live-stream connectivity and playback endpoints are specified in the [streaming design](streaming-design.md). They return gateway health or a short-lived browser-safe playback URL, never RTSP credentials.

## Alerts

| Method | Path | Minimum role |
| --- | --- | --- |
| GET | `/stores/:storeId/alerts` | operator |
| GET | `/stores/:storeId/alerts/:alertId` | operator |
| POST | `/stores/:storeId/alerts/:alertId/acknowledge` | operator |
| POST | `/stores/:storeId/alerts/:alertId/dismiss` | operator |
| POST | `/stores/:storeId/alerts/:alertId/resolve` | manager |
| GET | `/stores/:storeId/alerts/:alertId/evidence/:evidenceId/playback-url` | operator |

`GET /alerts` supports `status`, `severity`, `type`, `subjectPersonCategory`, `cameraId`, `from`, `to`, `cursor` and `limit`. It sorts by `detectedAt` descending with cursor pagination. `correlationId` groups observations of the same incident across cameras.

Alerts are never hard-deleted once they have notification deliveries - the database enforces this via `ON DELETE RESTRICT` on the delivery foreign key. Hiding or archiving such an alert is handled through alert status in a later task; no hard-delete endpoint is exposed.

## Internal AI ingestion

`POST /api/v1/internal/ai/alerts` is the service-to-service ingestion boundary implemented in task 6. It is disabled unless `AI_INGEST_TOKEN` is configured and accepts that opaque secret only as a Bearer token. The token must contain at least 32 characters and is never logged or returned.

The request contains `sourceEventId`, optional incident-level `correlationId`, `storeId`, an enabled `cameraId`, optional enabled `zoneId`, `type`, `severity`, optional `confidence`, detection timestamps, object metadata and one to five relative video evidence keys. Evidence accepts only `video/mp4` or `video/webm`; URLs, absolute paths, traversal segments and duplicate keys are rejected. Repeating the same `sourceEventId` returns the original alert and route deliveries with `200` and creates no new rows.

When an ingested alert matches an enabled notification rule, the alert transaction also creates an idempotent notification delivery. Telegram is the first delivery route; a configured WhatsApp route waits as fallback. The worker executes retries/fallback and records immutable attempts; both Telegram Bot API and WhatsApp Cloud API adapters are registered when the worker is enabled. See [notification-design.md](notification-design.md).

The alert UUID, all evidence rows and all matching outbox deliveries commit atomically. A storage-key conflict or enqueue failure rolls the entire request back. Alert list/detail responses include `hasVideoEvidence`; dashboard clients use it to retain real evidence-backed alerts and request a fresh authenticated playback URL when the owner opens one.

Notification configuration resources follow the soft-disable policy: `DELETE` removes an endpoint or rule only when no delivery references it, and otherwise responds `409` so the caller can disable it with `PATCH`. The endpoint test-message route records its send as an explicit TEST-kind delivery with the same immutable attempt history, even though no real alert exists.

## Notification configuration (implemented in task 3)

All routes below require an authenticated store **OWNER**. Resources outside the caller's store return `404`.

| Method | Path | Purpose |
| --- | --- | --- |
| GET, POST | `/stores/:storeId/notification-endpoints` | List or create Telegram/WhatsApp endpoint configuration. |
| PATCH, DELETE | `/stores/:storeId/notification-endpoints/:endpointId` | Update or delete an endpoint without delivery history. |
| POST | `/stores/:storeId/notification-endpoints/:endpointId/test` | Enqueue a TEST-kind delivery; returns `202` with only the delivery id and status. |
| GET, POST | `/stores/:storeId/notification-rules` | List or create emergency routing rules. |
| PATCH, DELETE | `/stores/:storeId/notification-rules/:ruleId` | Update or delete a rule without delivery history. |
| GET, POST | `/stores/:storeId/notification-rules/:ruleId/channels` | List or add ordered channel routes for a rule. |
| PATCH, DELETE | `/stores/:storeId/notification-rules/:ruleId/channels/:channelId` | Update priority/delay/enabled flag or delete a channel without delivery history. |
| GET | `/stores/:storeId/notification-deliveries` | OWNER-only filtered/paginated delivery audit list. |
| GET | `/stores/:storeId/notification-deliveries/:deliveryId` | OWNER-only delivery detail with immutable attempt log. |
| GET, POST | `/webhooks/whatsapp` | Public Meta verification handshake and HMAC-verified WhatsApp delivery receipts. |

Behavior implemented in task 3:

- Endpoint responses replace `credentialRef` with `credentialConfigured: true/false` and mask destinations (`destinationMasked`, last four characters only). `credentialRef` must use `env://` or `render-secret://`; raw Telegram tokens (`123456789:AAE...`) and Meta tokens (`EAA...`) are rejected. `config` rejects `token`/`secret`/`password`/`credential` keys and token-like values.
- Provider is immutable after creation; PATCH bodies with unknown fields (including `provider`/`endpointId`) are rejected as are empty bodies. Enabling WHATSAPP requires a decimal `providerAccountRef` (Meta Phone Number ID), an E.164 `destinationRef`, a decimal `config.wabaId`, the exact Meta-approved `emergency_security_alert` template with `config.templateLanguage=en`, a supported internal template contract, plus RFC3339 `optIn.capturedAt`, `optIn.source` and `optIn.policyVersion`; violations are 400, never a Meta API call. Version 2 is the default for new endpoints and deliveries. Version 1 remains accepted for immutable legacy delivery snapshots during the compatibility window.
- Rule matching is `minimumSeverity AND alertTypes` with empty `alertTypes` matching every type that passes severity. Duplicate alert types are normalized. Create defaults follow the migration: omitted fields become `minimumSeverity=CRITICAL`, `alertTypes=[]`, `cooldownSeconds=0`, `isEnabled=true`; PATCH stays partial and never reapplies create defaults. Nonzero `cooldownSeconds` is evaluated transactionally against the exact incident fingerprint; emergency rules remain at zero so independent emergencies are never suppressed.
- Channels order routes by ascending `priority` (lower routes first); duplicate rule+endpoint or duplicate priority within a rule returns `409`.
- `POST .../test` requires a UUID `requestId`, is idempotent per `requestId`, enqueues only an outbox row pinned to `endpointId` (no provider call), refuses disabled endpoints with `409`, and unknown/cross-store endpoints return `404`.
- Validation failures return `400`; duplicate destination/priority/endpoint conflicts and delete-blocked history return `409`.
- ALERT dedupe is incident-scoped: the key `alert:{correlationId|alertId}:{ruleId}:{endpointId}` excludes templateVersion, so one correlated incident yields a single delivery per rule+endpoint even across cameras or template changes, while distinct incidents stay independent. Camera observations remain separate alerts/evidence for review.
- Task 5 implements the worker, retry/backoff, fallback activation, lease recovery, cooldown evaluation, immutable attempt logs, delivery-history APIs, HMAC-verified WhatsApp receipt webhooks and evidence-scoped expiring review links. It does not make the worker active unless `NOTIFICATION_WORKER_ENABLED=true`.
- Task 4 supplies both Telegram Bot API and WhatsApp Cloud API provider adapters. The active Meta-approved WhatsApp version 2 contract uses the `emergency_security_alert` `en` template with a secure HTTPS video header, four reviewed body parameters and one dynamic URL button parameter containing only the alert ID. Version 2 is the default for new deliveries; version 1 remains legacy snapshot support only. Missing or unsafe video links fail closed so fallback behavior remains auditable.

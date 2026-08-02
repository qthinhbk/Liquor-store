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

## Internal AI ingestion

`POST /internal/ai/alerts` is reserved for task 6. Its body includes `sourceEventId`, `storeId`, `cameraId`, optional `zoneId`, `type`, `severity`, `confidence`, detection timestamps, metadata and evidence object keys. Repeating the same `sourceEventId` must be idempotent.

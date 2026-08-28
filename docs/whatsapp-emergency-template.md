# WhatsApp emergency alert template

## Status and scope

This document freezes the first owner-facing WhatsApp copy before provider integration begins.

- Meta template creation is currently blocked until the `Ketch Enterprise AI` Business Portfolio restriction is cleared and a WhatsApp Business Account (WABA) exists.
- This task defines copy, parameter order, sample values, endpoint metadata and opt-in language only. It does not add WhatsApp client code, migrations, credentials, commits or deployment changes.
- Version 1 is text-only. A `Review alert` URL button is intentionally deferred until the secure, short-lived alert-link flow is implemented. No unsigned dashboard or evidence URL may be placed in the template.

## Meta template definition

| Meta field | Value |
| --- | --- |
| Name | `emergency_security_alert` |
| Internal version | `whatsapp-emergency-security-alert-v1` |
| Category | `UTILITY` |
| Language | `en_US` |
| Header type | Text |
| Header | `Emergency security review` |
| Footer | `Ketch Enterprise AI` |
| Buttons | None in version 1 |

### Body

```text
A potential emergency was detected at {{1}}.

Camera: {{2}}
Detected: {{3}}
Event: {{4}}

Please open the Ketch dashboard, review the available footage, and follow your store's emergency procedure. This automated alert does not contact emergency services.
```

The copy deliberately says `potential emergency`. It must not claim that a weapon, theft or other crime definitely occurred, and it must not imply that the system contacted law enforcement.

### Parameter contract and Meta sample values

| Position | Payload source | Required rendering rule | Sample submitted to Meta |
| --- | --- | --- | --- |
| `{{1}}` | `store.name` | Owner-visible store name | `Liquor Store` |
| `{{2}}` | `camera.name` | Owner-visible camera name; fallback `Unknown camera` | `Whole store` |
| `{{3}}` | `alert.detectedAt` + `store.timezone` | Format once in the store timezone and include the timezone abbreviation | `Aug 24, 2026, 9:42 PM CDT` |
| `{{4}}` | Owner-safe alert type label | For the initial rule use `Possible violence or weapon detected` | `Possible violence or weapon detected` |

Render parameters as plain text. Strip control characters and collapse unexpected line breaks before calling Meta. Do not render AI confidence, raw metadata, person identity, RTSP URLs, camera credentials, object-storage keys or provider secrets.

## Emergency-only behavior

The initial notification rule remains `minimumSeverity = CRITICAL`, `alertTypes = ['WEAPON_DETECTED']` and `cooldownSeconds = 0`.

- Suspicious-behavior alerts stay in the dashboard and do not use this template.
- Emergency notifications do not expose `Match` or `False alarm` actions in WhatsApp.
- The owner reviews the evidence and follows the store procedure; the system never contacts police automatically.
- Every independent emergency gets its own delivery. Existing `dedupeKey` semantics prevent only duplicate enqueue for the same incident.

## Endpoint configuration contract

For a WhatsApp `notification_endpoints` row:

| Field | Meaning |
| --- | --- |
| `providerAccountRef` | Meta Phone Number ID used to send the message. |
| `destinationRef` | Opted-in owner phone number in E.164 format. Treat it as personal data and mask it in API responses and logs. |
| `credentialRef` | Secret-manager reference such as `render-secret://whatsapp/cloud-api/access-token`; never the token itself. |
| `config.wabaId` | Non-secret WhatsApp Business Account ID. |
| `config.templateName` | `emergency_security_alert`. |
| `config.templateLanguage` | `en_US`. |
| `config.templateVersion` | `whatsapp-emergency-security-alert-v1`. |
| `config.optIn` | Consent evidence described below. |

Recommended non-secret `config` shape:

```json
{
  "wabaId": "<WABA_ID>",
  "templateName": "emergency_security_alert",
  "templateLanguage": "en_US",
  "templateVersion": "whatsapp-emergency-security-alert-v1",
  "optIn": {
    "capturedAt": "<ISO_8601_TIMESTAMP>",
    "source": "OWNER_DASHBOARD",
    "policyVersion": "whatsapp-emergency-alerts-v1"
  }
}
```

The WABA ID and Phone Number ID are identifiers, not access credentials. The Cloud API access token and App Secret must remain only in the deployment secret manager.

This `config` object is compatible with the existing schema: `notification_endpoints.config` is a `JSONB NOT NULL DEFAULT '{}'::jsonb` column whose `notification_endpoints_config_object_check` constraint requires a JSON object, which this shape satisfies. No new migration is needed, and future additive keys are ignored until the sender task reads them.

## Owner opt-in copy

Display an unchecked consent control before enabling the WhatsApp endpoint:

```text
I agree to receive time-sensitive WhatsApp emergency security alerts from Ketch Enterprise AI for my store. Messages may include the store, camera, incident type and detected time. I can disable WhatsApp alerts at any time in notification settings.
```

Store the consent timestamp, source and policy version in endpoint configuration. Enabling Telegram must not implicitly opt the owner into WhatsApp. Disabling the endpoint must stop new WhatsApp deliveries without deleting delivery history.

## Submission checklist after Meta removes the restriction

1. Create the test WABA and test phone number in the app's WhatsApp setup.
2. Add and verify an opted-in test recipient without exposing the temporary token in screenshots or chat.
3. In WhatsApp Manager, create a `UTILITY` template named `emergency_security_alert` in `English (US)`.
4. Paste the header, body and footer exactly as documented and enter the four sample values above.
5. Submit for review and record the template ID, resulting category and status. Never record an access token in this repository.
6. Do not enable production delivery until the template status is `APPROVED` and the sender task validates the exact name, language and parameter count.

## Future secure-link revision

After the secure video-link task exists, submit a new reviewed template revision with a single `Review alert` URL button. The URL must be short-lived, scoped to one alert and recipient, HTTPS-only and revocable. It must never expose an object-storage key, bearer token, RTSP URL or camera credential. Snapshot the new internal version into each delivery so queued version-1 messages remain renderable.

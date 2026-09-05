# Backend security fixes — 05/09/2026

Status: deployed to production on 05/09/2026 at 17:55 GMT+7. Neon migration and Render rollout completed under the user's deployment request. Application commit: `576b561a1a102e3a9e331afbda9332bf1c89b721`; [Render deployment](https://dashboard.render.com/web/srv-d9u5cd740ujc73fj6m70/deploys/dep-dadv9ocs728c73fipgo0) reports **Deploy succeeded / Live**. This follows [the audit](security-audit-2026-09-05.md) at baseline `0b0468f`.

Production verification: Go 1.26.8 selected with `GOTOOLCHAIN=go1.26.8`; API and notification worker started successfully with secure review links enabled. Two exact credential bindings were saved and checked against Neon. `TRUST_PROXY=false` is the deployed safe fallback because trusted ingress CIDRs have not been established; login limits currently use the TCP peer and may be shared by clients behind the same ingress. No trusted ranges were guessed.

Seven non-destructive production checks passed: API and frontend proxy health 200; unauthenticated stores/AI ingestion and unsigned WhatsApp webhook 401; login without trusted Origin 403; Swagger 404. Neon verification retained one store, 21 alerts, two deliveries, three attempts and no queued delivery. No test notification or WhatsApp retry was sent.

## Implemented changes

| Finding | Final behavior | Verification |
|---|---|---|
| SEC-01: self-created OWNER | Every protected route checks the account is ACTIVE. Creating another store requires an existing OWNER membership. The membership/account are locked inside the creation transaction; registration remains separately gated. | OPERATOR rejected; suspended owner's read/create/profile requests rejected; active owner can create a store. |
| SEC-02: arbitrary environment credential | Exact store/provider/provider-account/reference must match a binding managed in deployment configuration. Both API and sender enforce it. Provider-specific environment names are additionally restricted. Worker startup checks all enabled endpoints before consuming any queued jobs. | Unrelated JWT secret, wrong store/provider/account, empty policy and unbound enabled endpoint rejected; authorized mock send succeeds. No real secrets/provider calls used. |
| SEC-03: overwritten alert decision | OPERATOR cannot change RESOLVED or DISMISSED. MANAGER/OWNER can correct a terminal decision to the other terminal state. Repeated decisions and invalid transitions return 409; row locks serialize decisions. Before/after snapshots are stored atomically in append-only history. | Two concurrent owner corrections produce one 201 and one 409, with exactly one history entry. Original actor/note preserved. History UPDATE/DELETE/TRUNCATE rejected. |
| SEC-04: unlimited TEST jobs | PostgreSQL serializes admission across API instances. Quotas cover endpoint, store, requester and global volume/pending jobs. Repeated requestId returns the same delivery. Worker claims ALERT before TEST. | Twelve concurrent distinct requests produce one delivery and eleven 429 responses; repeating the accepted request does not insert another job. An older TEST does not displace ALERT with batch size 1. |
| SEC-05: spoofed forwarded IP | Trust forwarded headers only from a peer in configured proxy CIDRs; parse the chain and select the first untrusted address from the right. Normalize IPv4-mapped IPv6. Empty/malformed configuration does not trust the header. | Original spoofing scenario now admits 10/100 requests into one bucket; direct-peer, malformed-chain, mapped-IP and multiple-hop cases covered. |
| DEP-01: vulnerable local Go | Required Go minimum is now 1.26.8; scripts allow automatic toolchain selection and support a 1.26.8 portable SDK. | Unit tests, vet, build and govulncheck run with Go 1.26.8. |

### Alert transition policy

- NEW or ACKNOWLEDGED → DISMISSED: OPERATOR or above.
- NEW → ACKNOWLEDGED: OPERATOR or above.
- NEW or ACKNOWLEDGED → RESOLVED: MANAGER or OWNER.
- RESOLVED ↔ DISMISSED: MANAGER or OWNER only, retaining the old decision in history. This preserves the authorized `Confirmed by mistake?` workflow.
- Repeating the current status, or moving a terminal decision back to ACKNOWLEDGED: 409, no attribution rewrite.
- Omitting `note` preserves the prior note. Explicitly clearing/replacing it still retains the old note in history.

History rows deliberately do not cascade when an alert, store or user is deleted. Runtime database permissions should allow INSERT/SELECT only on this table. The trigger prevents ordinary UPDATE/DELETE/TRUNCATE; a database administrator who can change schema can still disable controls. History starts with this deployment; previous decisions that were already overwritten cannot be reconstructed automatically.

### TEST delivery limits

| Scope | New TEST limit | Maximum outstanding TEST jobs |
|---|---|---|
| Endpoint | 1/minute and 3/hour | 1 |
| Store | 10/hour | 3 |
| Requesting user across stores | 10/hour | Covered by endpoint/store/global caps |
| Entire service | 100/hour | 50 |

Outstanding means PENDING, PROCESSING or RETRY_SCHEDULED. Quotas use database time and include failed/completed tests within the time window. Distinct request IDs cannot evade limits. The API supplies `requestedById` from the authenticated principal, never the request body. Rejection returns 429 and a conservative `Retry-After: 3600`.

ALERT jobs bypass these TEST quotas and are selected before TEST jobs. This prioritization is not a guarantee of fixed end-to-end delivery latency or a general provider billing cap for all ALERT traffic. Actual notification delivery still depends on the worker, provider limits, Meta billing and recipient eligibility.

## Deployment requirements

### 1. Apply the additive database migration first

Migration: `apps/api-go/internal/migrations/sql/20260905000000_security_controls.sql`.

It adds `alert_status_history`, immutable-history triggers, a nullable `requestedById` column on notification deliveries and indexes for quota/priority queries. It does not rewrite existing alerts or send notifications. Apply through the existing migration command using the intended direct database connection, after normal deployment approval. The API does not auto-migrate at startup.

Deploying the new API before this migration will make alert decision/history and TEST-enqueue operations fail. Migration was verified on a new disposable PostgreSQL instance and re-running it reported that the database was already up to date. The migration has now also been applied to Neon: history table, requester column, both immutable-history triggers and all five quota/priority indexes were confirmed. Counts remained one store, 21 alerts, two deliveries, with no queued delivery at verification time.

### 2. Configure credential bindings before enabling the new worker

New environment setting: `NOTIFICATION_CREDENTIAL_BINDINGS`, a JSON array. For a single store with Telegram and WhatsApp, use the actual values of `storeId`, `provider`, `providerAccountRef` and `credentialRef` already provisioned in `notification_endpoints`:

```json
[
  {
    "storeId": "<actual-store-uuid>",
    "provider": "TELEGRAM",
    "providerAccountRef": "",
    "credentialRef": "env://TELEGRAM_BOT_TOKEN"
  },
  {
    "storeId": "<same-actual-store-uuid>",
    "provider": "WHATSAPP",
    "providerAccountRef": "<actual-meta-phone-number-id>",
    "credentialRef": "env://WHATSAPP_ACCESS_TOKEN"
  }
]
```

This setting contains references and IDs, not token values. Tokens remain in their existing deployment secret variables. Preserve the exact provider account reference: if Telegram's stored value is a bot label instead of empty, the binding must match that label. WhatsApp binds the Phone Number ID, not the WABA ID. Provider-specific suffixed variables can be used when explicitly assigned; arbitrary variables and wildcard bindings are denied.

Missing/empty bindings with `NOTIFICATION_WORKER_ENABLED=true` fail startup. A partial binding configuration that does not cover every enabled endpoint also fails startup before the worker claims jobs. With the worker disabled, other API operations remain available; unbound notification credential changes/test sends return 403. Existing unbound endpoints can still be disabled with `PATCH isEnabled=false`.

The provisioning command does not automatically authorize arbitrary references: deployment bindings must be configured independently. Before enabling or changing notification routing, include the intended binding. Revoking a binding requires restarting/redeploying processes that loaded the previous configuration.

Live provider smoke tests additionally require `NOTIFICATION_TEST_STORE_ID` and the same configured bindings. Those tests were not run during this patch.

### 3. Set the actual trusted proxy ranges

`TRUST_PROXY=true` alone no longer trusts X-Forwarded-For. Configure `TRUSTED_PROXY_CIDRS` as comma-separated actual proxy CIDRs, after verifying the ingress chain. Do not guess Render/Cloudflare ranges or use `0.0.0.0/0` / `::/0`.

If proxy CIDRs are empty, the API uses its TCP peer for rate limiting. This is safe against header spoofing but can put clients behind the same ingress into one rate-limit bucket, so production ingress configuration must be checked before rollout. Malformed ranges fail configuration; malformed forwarded chains fall back to the peer IP.

### 4. Build with a patched toolchain

The module requires Go 1.26.8 or newer. Windows helpers use `GOTOOLCHAIN=auto`, allowing an older launcher to download/select the module's patched toolchain. No global system Go installation was replaced. Pin a supported patched version in deployment tooling and inspect the actual built binary; do not infer production's Go version from local `go.mod` alone.

Go 1.26.8 is available on the [official Go downloads page](https://go.dev/dl/). `govulncheck` on the patched source reports no affected symbols or imported-package vulnerabilities. It still reports module-only advisories in packages this backend does not call; that is not a promise that every package in every dependency module is advisory-free.

## Verification performed

- `go test -count=1 ./...`: pass with all live/production/database flags disabled.
- `go vet ./...`: pass.
- `go build ./...`: pass.
- Disposable PostgreSQL 14 in WSL, listening on loopback only: migration apply and idempotent rerun, API security/concurrency regressions, notification configuration, AI ingestion, existing notification runtime suite and ALERT-priority regression all pass.
- Provider behavior used mock HTTP servers; no real Telegram/WhatsApp message was sent.
- The existing AI integration fixture was corrected to omit `status` from camera creation, matching the actual API contract.
- The existing secure-link expiry test now explicitly expires its database row instead of sleeping against the independent Windows clock. This preserves the expiry assertion and avoids host/WSL clock drift.
- Existing worker concurrency fixture now uses separate endpoints, consistent with the new one-pending-test-per-endpoint rule.

Neon migration and application deployment are complete, with production checks recorded above. Meta error `131042` remains an external billing issue and was not retried or resolved by these security patches.

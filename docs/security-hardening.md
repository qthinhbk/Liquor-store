# Security hardening

## Implemented controls

- Argon2id password hashing with bounded PHC parameters and constant-work invalid-user login handling.
- Short-lived JWT access tokens validated with an issuer, audience, issued-at time, expiry, and HS256 only.
- Opaque refresh tokens stored as SHA-256 hashes and rotated atomically on every refresh.
- `HttpOnly` refresh cookies; production cookies also require HTTPS and use `SameSite=None` for the cross-site Vercel/Render deployment.
- Origin validation for all authentication POST endpoints to protect cookie-backed operations from CSRF.
- Per-IP authentication rate limits: login 10/minute, refresh/logout 30/minute, registration 3/hour.
- Registration and member-management routes disabled by default.
- Swagger disabled by default in production and authorization persistence disabled in development Swagger.
- Strict CORS allowlist, one-megabyte JSON body limit, parameterized SQL, store-scoped RBAC, CSP, anti-framing, no-sniff, no-referrer, permissions policy, and no-store auth responses.
- Frontend access tokens remain memory-only; post-login navigation is restricted to known internal routes.
- Production Vercel security headers are defined in `apps/web/vercel.json`.

## Required deployment settings

```dotenv
NODE_ENV=production
WEB_ORIGIN=https://your-dashboard-domain.example
JWT_ACCESS_SECRET=<at-least-32-random-characters>
JWT_ISSUER=liquor-store-security-api
JWT_AUDIENCE=liquor-store-owner-dashboard
REGISTER_ENABLED=false
MEMBER_MANAGEMENT_ENABLED=false
SWAGGER_ENABLED=false
TRUST_PROXY=true
```

Set `TRUST_PROXY=true` only together with `TRUSTED_PROXY_CIDRS` containing the actual ingress networks. Forwarded headers are ignored unless the TCP peer is trusted; chains are validated and walked from right to left to select the first untrusted address. Empty proxy CIDRs fall back to the TCP peer even when TRUST_PROXY is true. Do not use all-address ranges or assume a header is trustworthy because it exists. Add an upstream rate limit/WAF because the in-memory auth limiter is per API instance.

## September 2026 application fixes

- Protected API routes check that the JWT subject still exists and is ACTIVE. Additional stores require an existing active OWNER; OPERATOR-only users cannot bootstrap themselves into OWNER.
- Alert decisions are serialized in a transaction. OPERATOR cannot change terminal RESOLVED/DISMISSED decisions; management corrections retain immutable before/after history in `alert_status_history`. UPDATE, DELETE and TRUNCATE of this table are rejected by triggers. Database administration/migration privileges can still change schema and must be isolated from the runtime role.
- Notification credential references must match a server-managed `NOTIFICATION_CREDENTIAL_BINDINGS` entry for the exact store, provider and provider account. Provider-specific environment names are additionally restricted. The API and sender both enforce the binding; an unconfigured worker refuses startup.
- TEST delivery admission uses a PostgreSQL advisory transaction lock and per-endpoint/store/user plus global quotas. ALERT jobs are selected before TEST jobs.
- Go 1.26.8 is the minimum build version.

See [security-fixes-2026-09-05.md](security-fixes-2026-09-05.md) for migration order, binding configuration and test results. These changes are local until deployed.

## Manual actions before public deployment

1. Rotate the Neon password, Owner password, and JWT secret because earlier development credentials were shared during setup.
2. Use separate Neon roles: an owner/migration role for migrations and a least-privilege runtime role for the API.
3. Configure automated Neon backups and test restore procedures.
4. Add MFA, password change/recovery, global session revocation, and immutable audit events before handling high-risk real-world alerts.

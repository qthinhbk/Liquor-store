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

Set `TRUST_PROXY=true` only when the API is reachable exclusively through a trusted reverse proxy such as Render's ingress. Add an upstream rate limit/WAF because the in-memory limiter is per API instance.

## Manual actions before public deployment

1. Rotate the Neon password, Owner password, and JWT secret because earlier development credentials were shared during setup.
2. Use separate Neon roles: an owner/migration role for migrations and a least-privilege runtime role for the API.
3. Configure automated Neon backups and test restore procedures.
4. Add MFA, password change/recovery, global session revocation, and immutable audit events before handling high-risk real-world alerts.

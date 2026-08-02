# Authentication and authorization design

## Session model

1. `POST /api/v1/auth/register` creates the first user, store and `OWNER` membership in one transaction.
2. `POST /api/v1/auth/login` validates email and password.
3. The API returns a signed JWT access token (15 minutes) and sets one opaque refresh token in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie.
4. `POST /api/v1/auth/refresh` rotates the refresh token: revoke the old `RefreshSession`, create a new one and return a new access token.
5. `POST /api/v1/auth/logout` revokes the current refresh session and clears its cookie.

Passwords will use Argon2id in task 3. JWT signing secrets are environment variables, never database fields or source-controlled values.

## Authorization

Every authenticated request carries `Authorization: Bearer <access-token>`. The access token has `sub` (user ID) only; store roles are loaded server-side through `StoreMembership` to avoid stale permissions after role changes.

| Role | Store access | Alert triage | Camera and zone configuration | Manage members |
| --- | --- | --- | --- | --- |
| OWNER | Full | Yes | Yes | Yes |
| MANAGER | Assigned store | Yes | Yes | No |
| OPERATOR | Assigned store | Acknowledge / dismiss | Read only | No |

AI ingestion is not a user session. It will use a separate internal service credential or request signature when implemented in task 6.

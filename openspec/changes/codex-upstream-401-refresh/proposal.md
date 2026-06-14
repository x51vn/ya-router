## Why

When the Codex upstream returns HTTP 401 (`unauthorized_unknown` / "Could not parse your authentication token"), the service currently passes the 401 straight through to the client with no recovery attempt. This silently breaks all subsequent requests until the service is restarted or the user explicitly intervenes, even though a valid refresh token may be available.

## What Changes

- On upstream 401, attempt to refresh the access token before failing the request.
- If the refresh succeeds, retry the request once with the new token.
- If the refresh fails with an unrecoverable error, set `authBroken=true` (existing permanent-failure path).
- If the refresh fails transiently, return the 401 to the caller (do not retry indefinitely).
- Log the 401-triggered refresh attempt distinctly from the normal expiry-triggered refresh.

## Capabilities

### New Capabilities

- `codex-upstream-401-recovery`: Detect upstream 401, trigger token refresh, and retry the request once with the new credential.

### Modified Capabilities

- `codex-auth-failure-state`: The existing permanent-failure (`authBroken`) path is now also reachable via upstream 401 + unrecoverable refresh error (not only from expiry-triggered refresh). Requirement delta: upstream 401 that cannot be recovered by a refresh MUST set `authBroken` and return 503 `provider_auth_broken`.

## Impact

- `src/codex_provider.go`: `ProxyRequest` — detect 401 response, call `EnsureAuthenticated` (force-refresh), retry once.
- `src/codex_auth.go`: No changes needed; existing `codexRefreshToken` and `errRefreshUnrecoverable` are reused.
- No API surface change; clients see either a successful retry response or the original 401 / 503.

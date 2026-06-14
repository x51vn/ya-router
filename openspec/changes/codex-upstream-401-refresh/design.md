## Context

Our service proxies requests to the Codex upstream (`chatgpt.com/backend-api/codex/responses`). Authentication uses a bearer access token obtained via the OpenAI device-code OAuth flow.

Currently, `EnsureAuthenticated` checks token expiry before proxying. If the token hasn't expired yet (based on our stored `ExpiresAt`), the request proceeds. However, the upstream may still reject the token with HTTP 401 `unauthorized_unknown` ("Could not parse your authentication token") in cases where:
- The token was invalidated server-side without our knowledge
- A race condition caused our persisted token to become stale
- The token was manually injected (e.g. during testing) and is structurally invalid

When this happens, the service passes the 401 through to the client with no recovery attempt. Subsequent requests repeat this failure indefinitely.

The official `openai/codex` CLI (Rust, `codex-rs/login/`) handles this by classifying 401 as a permanent refresh trigger — if upstream returns 401, it means the access token is bad regardless of what `ExpiresAt` says.

## Goals / Non-Goals

**Goals:**
- Detect upstream HTTP 401 responses from Codex and attempt token refresh before failing
- Retry the request exactly once with the refreshed token
- If refresh fails with an unrecoverable error, escalate to `authBroken` (existing path)
- Avoid infinite retry loops — one refresh + one retry per request
- Log the 401-triggered refresh distinctly from normal expiry-triggered refresh

**Non-Goals:**
- Changing the device-code flow itself (already working)
- Adding concurrency control / refresh semaphore (separate change)
- Handling non-401 upstream errors (e.g. 429, 500)
- Changing the `auth codex` CLI subcommand

## Decisions

### 1. Retry-on-401 in ProxyRequest (not in a middleware)

The 401 detection and retry lives inside `CodexProvider.ProxyRequest`, after the upstream call. This keeps the logic provider-specific and avoids coupling it to the generic proxy retry mechanism in `proxy.go`.

**Alternative considered**: Adding 401 handling in `proxy.go`'s retry loop. Rejected because the retry loop doesn't have access to provider-specific refresh logic, and Copilot provider handles 401 differently.

### 2. Force-refresh via EnsureAuthenticated with expired ExpiresAt

To trigger a refresh, temporarily set `auth.ExpiresAt = 0` before calling `EnsureAuthenticated` (which already handles the refresh path). This reuses existing refresh logic without duplication.

**Alternative considered**: Calling `codexRefreshToken` directly from ProxyRequest. Rejected because `EnsureAuthenticated` already has reload-from-disk guards, unrecoverable-error handling, and authBroken escalation. Duplicating that would be error-prone.

### 3. One retry maximum per request

After a 401 + successful refresh, retry the upstream call exactly once. If the retry also returns 401, pass through as a final failure (do NOT set `authBroken` for a second 401 — the token was just refreshed, so either the account is suspended or there's a deeper issue).

### 4. Only trigger on `unauthorized_unknown` or generic 401

Only attempt refresh-and-retry when the upstream returns HTTP 401. If the 401 body contains an error code that indicates a known permanent issue (e.g. account suspension), skip the retry.

## Risks / Trade-offs

- **[Risk] Double upstream call on bad tokens** → One extra request per 401 (acceptable; the alternative is permanent failure). Mitigated by the one-retry limit.
- **[Risk] Refresh itself triggers authBroken** → Acceptable; this is the designed escalation path. If a 401 leads to an unrecoverable refresh error, the service correctly marks auth as broken.
- **[Trade-off] Token expiry set to 0 is a hack** → The intent is clear (force refresh), and it's immediately restored if refresh fails. Minimal risk since it's under the provider lock.

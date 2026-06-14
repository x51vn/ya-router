## Context

`codex_auth.go::codexRefreshToken` performs up to 3 retry attempts on any non-200 refresh response, including permanent OAuth errors like `refresh_token_reused` (code `refresh_token_reused`) and `invalid_grant`. After all retries fail, `codex_provider.go::resolveAuth` calls `reloadFromOfficialStore()` as a last-ditch attempt, but if the official store also has no valid token (same stale credentials), `auth.AccessToken` is still non-empty (the expired token), so the error branch at line 262 is NOT taken. The request proceeds with the expired token, receives 401 from upstream (`token_expired`), and that 401 is surfaced to the client.

On every subsequent request, the same cycle repeats: 3 × retry loop (~10s total), failed reload, request with stale token, 401. There is no in-process memory of the auth failure, no circuit-breaker, and no mechanism to escalate the error to the administrator.

## Goals / Non-Goals

**Goals:**
- Immediately stop retrying when the refresh endpoint returns a known-permanent error code (`refresh_token_reused`, `invalid_grant`).
- After an unrecoverable refresh failure, mark the `CodexProvider` as auth-broken for the current process lifetime.
- When auth is broken, return a fast-fail structured 503 error to API clients on every request, instead of re-attempting the refresh loop.
- Log the permanent failure once clearly, with actionable remediation instructions (`auth codex`).

**Non-Goals:**
- Automatic re-authentication or prompting the user interactively from a server process.
- Persisting the auth-broken state across restarts (a restart with fresh credentials is the recovery path).
- Changing the retry behavior for transient network errors (connection refused, timeouts) — those should still retry.
- Modifying the Copilot provider's refresh path.

## Decisions

### 1. Classify refresh errors as permanent vs. transient at the HTTP-response level

**Decision**: Parse the JSON error body for the `code` field; treat `refresh_token_reused`, `invalid_grant`, and `token_expired` as permanent (no retry); treat everything else (network errors, 5xx, unknown 4xx) as transient (existing retry behaviour unchanged).

**Rationale**: These three codes are explicitly documented as requiring re-authentication — retrying them is guaranteed to fail and wastes ~10s per request. Keying on the `code` field is more robust than status code alone, since the server returns `401` for both transient and permanent failures.

**Alternative considered**: Treat all 401s as permanent. Rejected — a transient 401 (e.g. clock skew, brief token expiry race) should still retry.

### 2. Unrecoverable error sentinel in `CodexProvider`

**Decision**: Add `authBroken bool` and `authBrokenErr error` fields to `CodexProvider`. Set them atomically (under the existing provider mutex or a dedicated mutex) when `codexRefreshToken` returns an unrecoverable error. Check this flag at the top of the token-resolution path and return immediately.

**Rationale**: In-process flag is the simplest mechanism with zero external dependencies. A restart clears the flag, which is the correct recovery signal (operator runs `auth codex`, then restarts the container).

**Alternative considered**: Write a "needs-reauth" marker to disk. Rejected — adds I/O, coupling to filesystem paths, and migration complexity. The operator workflow is restart-based anyway.

### 3. Client-facing error format

**Decision**: Return HTTP 503 with an OpenAI-compatible JSON error body:
```json
{"error":{"message":"Codex authentication is broken. Re-authenticate with `auth codex` and restart the service.","type":"authentication_error","code":"provider_auth_broken"}}
```

**Rationale**: 503 signals the service is temporarily unavailable (correct — it will recover after restart + re-auth). Using the OpenAI error envelope keeps it consistent with how other upstream errors are surfaced and avoids breaking client parsers.

**Alternative considered**: Return 401 to clients. Rejected — 401 implies the *client's* credentials are wrong; the issue is the *server's* upstream credentials.

### 4. `codexRefreshToken` return value

**Decision**: Return a typed error (or sentinel error variable `errRefreshUnrecoverable`) from `codexRefreshToken` so the caller can distinguish permanent from transient failures without re-parsing the error message string.

**Rationale**: Avoids coupling callers to the error message text, which could change. A sentinel error or error type check is cheap and idiomatic in Go.

## Risks / Trade-offs

- **Risk**: Misclassifying a transient error as permanent (false positive) → `CodexProvider` permanently broken until restart.  
  **Mitigation**: Conservative whitelist — only `refresh_token_reused` and `invalid_grant` are treated as permanent. Unknown codes still retry.

- **Risk**: `reloadFromOfficialStore()` returns a valid fresh token (e.g., operator manually re-ran `auth codex` on disk) but the auth-broken flag prevents using it.  
  **Mitigation**: The `reloadFromOfficialStore` path runs *before* setting the auth-broken flag. If it succeeds, the flag is never set. Restart is still required if the flag is already set — acceptable UX for a server process.

- **Trade-off**: Setting the flag on the first permanent failure means a brief window where one in-flight request triggers the flag and all concurrent requests immediately see 503. This is the correct behaviour: the auth is broken and continuing to serve stale data is worse.

## Migration Plan

1. Deploy new binary — no config changes required.
2. If auth is already broken: `auth codex` to re-authenticate, then `docker restart github-copilot-svcs`.
3. Rollback: deploy previous binary. No state to migrate.

## Open Questions

- None. The error codes in the logs (`refresh_token_reused`) are deterministic; the fix is straightforward.

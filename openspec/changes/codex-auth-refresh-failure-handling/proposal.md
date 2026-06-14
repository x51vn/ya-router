## Why

When the Codex refresh token is invalidated externally (e.g., `refresh_token_reused` from a concurrent login elsewhere), the service retries the unrecoverable 401 three times per startup **and** re-runs the full 3-attempt retry loop on every subsequent request (~10s delay each), then silently proceeds with the expired access token and returns 401 upstream to the caller. There is no permanent-failure short-circuit, no fast-fail on known-unrecoverable error codes, and no provider-level auth-state tracking to avoid repeated futile refresh attempts.

## What Changes

- **Fast-fail on unrecoverable refresh errors**: when the refresh endpoint returns `refresh_token_reused`, `invalid_grant`, or similar permanent OAuth error codes, stop retrying immediately (no 3-attempt loop) and return an unrecoverable error.
- **Provider-level auth-failure state**: after an unrecoverable refresh failure, mark the Codex provider as permanently auth-broken for the lifetime of the process, so subsequent requests fail immediately with a clear `503 Service Unavailable` instead of re-running the expensive retry loop.
- **Clear client-facing error**: requests to Codex while auth is broken return a structured JSON error (consistent with OpenAI error format) explaining that re-authentication is required, rather than proxying a confusing 401 from upstream.
- **Log improvement**: log the unrecoverable error at startup once, clearly distinguishing it from transient network failures.

## Capabilities

### New Capabilities

- `codex-auth-failure-state`: Tracks permanent Codex auth-broken state at the provider level; exposes fast-fail on subsequent requests and surfaces a clear re-auth error to API clients.

### Modified Capabilities

<!-- No existing spec-level requirements are changing. -->

## Impact

- `src/codex_auth.go`: `codexRefreshToken` — add early-exit logic for unrecoverable error codes before the retry loop.
- `src/codex_provider.go`: `CodexProvider` struct — add an `authFailed bool` / `authFailedErr error` field; set it on unrecoverable refresh failure; check it at request time to return immediately.
- `src/codex_provider.go`: `resolveAuth` / request path — check auth-failed state before attempting refresh.
- No new dependencies, no config changes, no breaking API changes.
- Callers using Codex models will get a structured 503 error immediately instead of a 10-second wait + 401; the fix is transparent once re-auth is performed.

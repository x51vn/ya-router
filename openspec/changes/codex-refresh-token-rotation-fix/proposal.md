## Why

When the official Codex CLI refreshes a token and writes new credentials to `~/.codex/auth.json`, our service races against it using a stale refresh token — causing `refresh_token_reused` on every subsequent attempt. Additionally, two concurrent inbound requests can both attempt a token refresh simultaneously, causing the same collision. The official `openai/codex` (Rust) implementation prevents this with a disk-reload guard and a per-provider semaphore; our service lacks both.

## What Changes

- **Reload-before-refresh guard**: Before calling the OAuth token endpoint, reload credentials from `~/.codex/auth.json` and compare the stored refresh token. If the on-disk token already differs from what we hold in memory (i.e., another process already refreshed it), skip the HTTP call and use the refreshed token from disk.
- **Refresh mutex**: Serialize concurrent refresh attempts within the same process using a `sync.Mutex` on `CodexProvider`. Only the first goroutine executes the HTTP call; others wait and reuse the result.
- **Extended error code coverage**: Add `refresh_token_expired` and `refresh_token_invalidated` to the unrecoverable error code set, matching the official error classification in `classify_refresh_token_failure`.
- **Token persistence on refresh success**: Persist the new `refresh_token` returned by the OAuth endpoint back to `~/.codex/auth.json` immediately after a successful refresh (currently only `access_token`/`id_token` are used in-memory).

## Capabilities

### New Capabilities

- `codex-refresh-token-guard`: Reload-before-refresh guard and per-provider refresh mutex that prevent stale-token races between our service and the official Codex CLI, and between concurrent goroutines within our service.

### Modified Capabilities

<!-- None — the `codex-auth-failure-state` spec requirements do not change; the fix here is at the transport level, below spec-visible behavior. -->

## Impact

- `src/codex_auth.go`: `codexRefreshToken` gains a pre-call reload+compare step.
- `src/codex_provider.go`: `CodexProvider` gains a `refreshMu sync.Mutex`; `EnsureAuthenticated` wraps the refresh call in the mutex; `unrecoverableRefreshCodes` map extended.
- `src/codex_auth_test.go` / `src/codex_provider_test.go`: New tests for the guard and mutex behaviour.
- No API surface changes; no config changes; no new dependencies.

## Context

Our service shares the `~/.codex/auth.json` file with the official Codex CLI. When the CLI refreshes tokens (e.g. when a user runs `codex` interactively), it writes a new `refresh_token` to disk. If our service then tries to refresh using the now-stale in-memory `refresh_token`, OpenAI's OAuth server returns `refresh_token_reused` — because each refresh token is single-use under token rotation.

Additionally, under concurrent load, two goroutines can both observe an expiring token and both attempt a refresh simultaneously. Only the first POST to `/oauth/token` succeeds; the second races against the already-consumed token and also gets `refresh_token_reused`.

Current state:
- `CodexProvider` has a single `sync.Mutex` (`mu`) guarding config mutation.
- `EnsureAuthenticated` holds `mu` for the duration, which serializes calls within the process — **the concurrent goroutine race is already prevented by `mu`**.
- However, `codexRefreshToken` reads `auth.RefreshToken` from in-memory state without first checking whether a fresher token has been written to `~/.codex/auth.json` by an external process.
- `unrecoverableRefreshCodes` covers `refresh_token_reused` and `invalid_grant` but misses `refresh_token_expired` and `refresh_token_invalidated`.

## Goals / Non-Goals

**Goals:**
- Before each OAuth refresh attempt, reload `~/.codex/auth.json` and compare the on-disk refresh token against the in-memory one; skip the HTTP call if the disk already holds a newer (already-rotated) token.
- Extend `unrecoverableRefreshCodes` to include `refresh_token_expired` and `refresh_token_invalidated`, matching official Codex error classification.
- Ensure no new external dependencies are introduced.

**Non-Goals:**
- Coordinating across multiple processes via file locking (flock/advisory). Reload-before-refresh eliminates the race without cross-process synchronization.
- Changing the token expiry heuristics, circuit-breaker, or retry counts.
- Intercepting or modifying the `persistToOfficialStore` write path beyond what's already there.

## Decisions

### Decision 1: Reload-before-refresh inside `codexRefreshToken`

**Chosen**: Add a `reloadFunc func() *CodexAuthState` parameter (or inline a call to `loadOfficialCodexAuth()`) at the top of `codexRefreshToken`. If the on-disk `refresh_token` differs from `auth.RefreshToken`, update `auth.RefreshToken` in-memory and proceed with the fresh token. If the on-disk token is already newer and the access token is also non-expired, skip the HTTP POST entirely.

**Alternative considered**: Put the reload in `EnsureAuthenticated` before calling `codexRefreshToken`. This is simpler but misses the case where `codexRefreshToken` is called independently (e.g. from tests or future subcommands).

**Rationale**: Keeping the guard inside `codexRefreshToken` makes the function self-contained and eliminates the race at the point of use. The cost is one `os.ReadFile` per refresh attempt, which is negligible compared to an HTTP round-trip.

### Decision 2: Skip HTTP call when on-disk token is already fresh

**Chosen**: If, after the reload, the on-disk access token is non-empty and not expiring (ExpiresAt > now+300), copy the on-disk credentials into `auth` and return nil immediately — no HTTP call needed.

**Rationale**: This directly mirrors the official Codex Rust implementation's `reload_if_account_id_matches()` guard. It handles the most common race scenario (CLI refreshed while our service was deciding to refresh) with zero network cost.

### Decision 3: Extend unrecoverable error codes

**Chosen**: Add `"refresh_token_expired"` and `"refresh_token_invalidated"` to `unrecoverableRefreshCodes`.

**Rationale**: Official `classify_refresh_token_failure` treats these identically to `refresh_token_reused` (all are permanent). Omitting them means we'd retry ~3× before giving up on a revoked token.

## Risks / Trade-offs

- **File read on every refresh**: `loadOfficialCodexAuth()` reads and JSON-parses `~/.codex/auth.json` on every refresh attempt. Under typical usage (one refresh per hour), this is imperceptible. Under a token-expiry storm (many concurrent requests all finding the token expired at once), `mu` already serializes them to one refresh attempt, so this is called at most once per expiry event.
- **Race window is narrow but not zero**: Between our reload and our POST, another process could refresh again. In that scenario we'd get `refresh_token_reused` once, which is already handled by the existing fast-fail logic (`errRefreshUnrecoverable` → `authBroken`). The reload guard eliminates the common case; the fast-fail handles the residual.
- **No change to `authBroken` semantics**: The guard reduces the probability of hitting `authBroken`, but the existing behavior (503 until restart) is unchanged for genuine token invalidations.

## Migration Plan

1. Merge the code change; existing auth state remains valid.
2. Deploy new image. No config changes required.
3. The reload guard activates automatically on next refresh attempt.
4. Rollback: redeploy previous image. No data migration needed.

## Open Questions

- None — the approach is fully determined by the research into the official Codex implementation.

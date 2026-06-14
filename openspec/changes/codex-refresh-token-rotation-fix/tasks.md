## 1. Extend unrecoverable error codes

- [x] 1.1 `src/codex_auth.go`: Add `"refresh_token_expired"` and `"refresh_token_invalidated"` to `unrecoverableRefreshCodes` map
- [x] 1.2 `src/codex_auth_test.go`: Add table cases for `refresh_token_expired` and `refresh_token_invalidated` in `TestIsUnrecoverableRefreshError`

## 2. Reload-before-refresh guard in codexRefreshToken

- [x] 2.1 `src/codex_auth.go`: At the top of `codexRefreshToken` (before the retry loop), call `loadOfficialCodexAuth()` and compare on-disk `RefreshToken` to `auth.RefreshToken`; if they differ, update `auth.RefreshToken` with the on-disk value
- [x] 2.2 `src/codex_auth.go`: If on-disk `AccessToken` is non-empty and `ExpiresAt > now+300`, copy all on-disk credentials into `auth` and return `nil` (skip HTTP call)
- [x] 2.3 `src/codex_auth.go`: If `loadOfficialCodexAuth()` returns an error or nil, proceed silently with existing in-memory state (non-fatal)

## 3. Tests for reload-before-refresh guard

- [x] 3.1 `src/codex_auth_test.go`: Add `TestCodexRefreshTokenUsesOnDiskTokenWhenDifferent` — mock official store with a different refresh token, confirm HTTP POST uses the on-disk token (inspect request body)
- [x] 3.2 `src/codex_auth_test.go`: Add `TestCodexRefreshTokenSkipsHTTPWhenOnDiskTokenFresh` — mock official store with a non-expiring access token, confirm no HTTP call is made and function returns nil
- [x] 3.3 `src/codex_auth_test.go`: Add `TestCodexRefreshTokenContinuesWhenOfficialStoreUnavailable` — point `CODEX_HOME` at nonexistent dir, confirm refresh proceeds normally (no panic, no error from missing store)

## 4. Verify and build

- [x] 4.1 `make fmt && make vet && make test && make build` — all pass

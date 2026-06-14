## 1. Core 401-retry logic in ProxyRequest

- [x] 1.1 `src/codex_provider.go`: After the upstream call returns, detect HTTP 401 response. If 401 and this is the first attempt (not already a retry): log `[codex] upstream 401 — refreshing token`, force-refresh by setting `auth.ExpiresAt = 0` under lock, call `EnsureAuthenticated`, then retry the upstream call once.
- [x] 1.2 `src/codex_provider.go`: If `EnsureAuthenticated` (force-refresh) returns an error wrapping `errRefreshUnrecoverable`, let the existing `authBroken` escalation handle it (already in EnsureAuthenticated). Return 503 via existing broken path.
- [x] 1.3 `src/codex_provider.go`: If `EnsureAuthenticated` (force-refresh) returns a transient error, return the original 401 response to the client.
- [x] 1.4 `src/codex_provider.go`: If the retry also returns 401, pass through the 401 to client without setting authBroken.

## 2. Tests

- [x] 2.1 `src/codex_provider_test.go`: Add `TestProxyRequestRetryOn401_RefreshSucceeds` — mock upstream to return 401 on first call, 200 on second call; verify client receives 200.
- [x] 2.2 `src/codex_provider_test.go`: Add `TestProxyRequestRetryOn401_RefreshFails` — mock upstream to return 401; mock refresh to fail with unrecoverable error; verify client receives 503 provider_auth_broken.
- [x] 2.3 `src/codex_provider_test.go`: Add `TestProxyRequestRetryOn401_RetryAlso401` — mock upstream to return 401 on both calls; verify client receives 401 (not 503, not another retry).

## 3. Verify and build

- [x] 3.1 `make fmt && make vet && make test && make build` — all pass

## 4. Deploy and verify

- [x] 4.1 Build and push docker image, update deployment, restart server
- [x] 4.2 Verify on server: service starts correctly, authBroken triggers on stale token, fallback to copilot works
- [x] 4.3 Verify on server: 401-retry logic tested via unit tests (production test requires interactive auth codex login)

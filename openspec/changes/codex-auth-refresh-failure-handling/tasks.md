## 1. Permanent Error Classification in codex_auth.go

- [x] 1.1 Define sentinel error variable `errRefreshUnrecoverable` in `codex_auth.go` to represent permanent, non-retryable refresh failures
- [x] 1.2 Define helper `isUnrecoverableRefreshError(body []byte) bool` that parses the JSON error body and returns `true` for error codes `refresh_token_reused` and `invalid_grant`
- [x] 1.3 In `codexRefreshToken`, after reading a non-200 response body, call `isUnrecoverableRefreshError`; if true, return `fmt.Errorf("...: %w", errRefreshUnrecoverable)` immediately without sleeping or retrying
- [x] 1.4 Verify existing retry path is unchanged for transient errors (connection errors, 5xx, unknown 4xx)

## 2. Auth-Broken State in CodexProvider

- [x] 2.1 Add fields `authBroken bool` and `authBrokenErr error` to the `CodexProvider` struct in `codex_provider.go`
- [x] 2.2 In `resolveAuth` (or the equivalent token-preparation path), after `codexRefreshToken` returns an error wrapping `errRefreshUnrecoverable`, check if `reloadFromOfficialStore` supplied a valid token; if not, set `p.authBroken = true` and `p.authBrokenErr = err`
- [x] 2.3 Log a single clear message when `authBroken` is first set: include the error code, the consequence ("all Codex requests will fail until restart"), and the recovery command (`./github-copilot-svcs auth codex`, then restart)

## 3. Fast-Fail on Broken Auth for Chat and Embeddings

- [x] 3.1 At the top of the Codex chat request handler (before any token resolution or upstream proxy call), check `p.authBroken`; if true, write an HTTP 503 JSON response in OpenAI error envelope format with `"code": "provider_auth_broken"` and return immediately
- [x] 3.2 Apply the same fast-fail check to the Codex embeddings request handler
- [x] 3.3 Confirm no upstream HTTP request is initiated when fast-fail triggers (log line should NOT appear for the upstream call)

## 4. Tests

- [ ] 4.1 Add table-driven test in `codex_auth_test.go` (or existing test file) for `isUnrecoverableRefreshError`: cases for `refresh_token_reused`, `invalid_grant`, transient 500 body, empty body, malformed JSON
- [ ] 4.2 Add test for `codexRefreshToken` confirming it returns immediately (no sleep) on `refresh_token_reused` and `invalid_grant` responses
- [ ] 4.3 Add test verifying `authBroken` is set after an unrecoverable refresh failure and that subsequent mock chat/embeddings requests return 503 `provider_auth_broken` without making upstream calls

## 5. Verification

- [ ] 5.1 Run `make fmt && make vet && make test && make build` — all pass with zero new failures
- [ ] 5.2 Manually verify (or via test) that a mock `refresh_token_reused` 401 response at startup causes a single log line with "re-authenticate" and `auth codex`, and all subsequent request handling skips the retry loop

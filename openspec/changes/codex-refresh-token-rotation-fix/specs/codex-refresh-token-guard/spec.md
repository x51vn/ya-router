## ADDED Requirements

### Requirement: Reload before refresh
Before sending a POST to the OAuth `/oauth/token` refresh endpoint, the service SHALL reload credentials from `~/.codex/auth.json`. If the on-disk `refresh_token` differs from the in-memory value, the service SHALL update the in-memory `refresh_token` with the on-disk value before proceeding.

#### Scenario: On-disk token is already fresh - skip HTTP call
- **WHEN** `codexRefreshToken` is called and the on-disk `access_token` is non-empty and `ExpiresAt > now + 300`
- **THEN** the service SHALL copy on-disk credentials into the in-memory auth state and return `nil` without making any HTTP request

#### Scenario: On-disk token is the same as in-memory - proceed normally
- **WHEN** `codexRefreshToken` is called and the on-disk `refresh_token` equals the in-memory `refresh_token`
- **THEN** the service SHALL proceed with the refresh HTTP POST using the existing token

#### Scenario: On-disk token is different - use fresh token
- **WHEN** `codexRefreshToken` is called and the on-disk `refresh_token` differs from the in-memory value but the on-disk `access_token` is expired or missing
- **THEN** the service SHALL use the on-disk `refresh_token` for the HTTP POST (not the stale in-memory one)

#### Scenario: Official store unavailable
- **WHEN** `~/.codex/auth.json` does not exist or cannot be parsed
- **THEN** the service SHALL proceed with the in-memory token unchanged (reload failure is non-fatal)

### Requirement: Extended unrecoverable error codes
The set of OAuth error codes that trigger immediate fast-fail (no retry, no authBroken state) SHALL include `refresh_token_expired` and `refresh_token_invalidated` in addition to the existing `refresh_token_reused` and `invalid_grant`.

#### Scenario: refresh_token_expired triggers fast-fail
- **WHEN** the OAuth endpoint returns a non-200 response with `"code": "refresh_token_expired"` in the JSON body
- **THEN** `codexRefreshToken` SHALL return immediately with an error wrapping `errRefreshUnrecoverable` without sleeping or retrying

#### Scenario: refresh_token_invalidated triggers fast-fail
- **WHEN** the OAuth endpoint returns a non-200 response with `"code": "refresh_token_invalidated"` in the JSON body
- **THEN** `codexRefreshToken` SHALL return immediately with an error wrapping `errRefreshUnrecoverable` without sleeping or retrying

#### Scenario: Transient error is still retried
- **WHEN** the OAuth endpoint returns a non-200 response with a JSON body that does not contain a known unrecoverable code
- **THEN** `codexRefreshToken` SHALL retry up to `maxRefreshRetries` times with backoff as before

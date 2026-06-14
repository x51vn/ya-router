## ADDED Requirements

### Requirement: Permanent refresh errors skip retry loop
When the Codex refresh endpoint returns a known-permanent OAuth error code (`refresh_token_reused` or `invalid_grant`), the service SHALL stop retrying immediately and return an unrecoverable error without waiting for the exponential backoff delays.

#### Scenario: refresh_token_reused skips all retries
- **WHEN** the refresh endpoint returns HTTP 401 with `"code": "refresh_token_reused"`
- **THEN** `codexRefreshToken` returns immediately after the first attempt with an unrecoverable error (no further attempts are made, no sleep delays occur)

#### Scenario: invalid_grant skips all retries
- **WHEN** the refresh endpoint returns HTTP 401 with `"code": "invalid_grant"`
- **THEN** `codexRefreshToken` returns immediately after the first attempt with an unrecoverable error

#### Scenario: transient network error still retries
- **WHEN** the refresh endpoint is unreachable (connection error or 5xx response)
- **THEN** `codexRefreshToken` retries up to `maxRefreshRetries` times with exponential backoff (existing behaviour unchanged)

#### Scenario: unknown 4xx still retries
- **WHEN** the refresh endpoint returns an HTTP 4xx with an unrecognised error code
- **THEN** `codexRefreshToken` retries up to `maxRefreshRetries` times (existing behaviour unchanged)

### Requirement: Provider tracks permanent auth-broken state
After `codexRefreshToken` returns an unrecoverable error that cannot be resolved by reloading credentials from disk, the `CodexProvider` SHALL set an internal auth-broken flag and store the triggering error.

#### Scenario: auth-broken flag set on unrecoverable refresh failure
- **WHEN** `codexRefreshToken` returns an unrecoverable error AND `reloadFromOfficialStore` does not supply a valid access token
- **THEN** `CodexProvider.authBroken` is set to `true` and `CodexProvider.authBrokenErr` is set to the unrecoverable error

#### Scenario: auth-broken flag not set on transient failure
- **WHEN** `codexRefreshToken` returns a transient (non-permanent) error
- **THEN** `CodexProvider.authBroken` remains `false`

#### Scenario: auth-broken flag cleared on process restart
- **WHEN** the service process is restarted with fresh credentials on disk
- **THEN** `CodexProvider.authBroken` starts as `false` (flag is not persisted across restarts)

### Requirement: Requests fast-fail when auth is broken
When the provider's auth-broken flag is set, all chat and embeddings requests to the Codex provider SHALL return immediately without attempting the refresh loop or proxying to the upstream API.

#### Scenario: chat request fails fast with 503
- **WHEN** `CodexProvider.authBroken` is `true` AND a chat completion request arrives
- **THEN** the request returns HTTP 503 with a JSON error body containing `"code": "provider_auth_broken"` and a human-readable message indicating that re-authentication is required
- **THEN** no upstream HTTP request is made
- **THEN** no refresh attempt is made

#### Scenario: embeddings request fails fast with 503
- **WHEN** `CodexProvider.authBroken` is `true` AND an embeddings request arrives
- **THEN** the request returns HTTP 503 with the same structured error

#### Scenario: elapsed time is negligible
- **WHEN** auth is broken and a request arrives
- **THEN** the response is returned in under 50ms (no network I/O)

### Requirement: Auth failure is logged once with remediation guidance
When the auth-broken state is first set, the service SHALL log a single prominent error message that identifies the error type and provides the exact command to recover.

#### Scenario: unrecoverable failure log at startup
- **WHEN** the refresh fails with `refresh_token_reused` during startup token resolution
- **THEN** the log contains the phrase "re-authenticate" and the command `auth codex` (or equivalent guidance)
- **THEN** no further refresh-loop error lines are emitted for subsequent requests (fast-fail prevents re-logging)

## ADDED Requirements

### Requirement: Retry on upstream 401

When the Codex upstream returns HTTP 401, the service SHALL attempt to refresh the access token and retry the request exactly once with the new credential.

#### Scenario: Upstream returns 401, refresh succeeds, retry succeeds
- **WHEN** a proxied request receives HTTP 401 from the Codex upstream
- **AND** token refresh succeeds (new access token obtained)
- **THEN** the service retries the same request once with the new token
- **AND** returns the retry response to the client

#### Scenario: Upstream returns 401, refresh succeeds, retry also returns 401
- **WHEN** a proxied request receives HTTP 401 from the Codex upstream
- **AND** token refresh succeeds
- **AND** the retry also returns HTTP 401
- **THEN** the service returns the 401 response to the client without further retry
- **AND** does NOT set authBroken (the token was just refreshed — issue is deeper)

#### Scenario: Upstream returns 401, refresh fails with unrecoverable error
- **WHEN** a proxied request receives HTTP 401 from the Codex upstream
- **AND** token refresh fails with an unrecoverable error (refresh_token_reused, invalid_grant, etc.)
- **THEN** the service sets authBroken = true
- **AND** returns HTTP 503 `provider_auth_broken` to the client

#### Scenario: Upstream returns 401, refresh fails with transient error
- **WHEN** a proxied request receives HTTP 401 from the Codex upstream
- **AND** token refresh fails with a transient error (network timeout, 5xx)
- **THEN** the service returns the original HTTP 401 to the client
- **AND** does NOT set authBroken

### Requirement: 401-triggered refresh is logged distinctly

The service SHALL log 401-triggered refresh attempts with a distinct prefix that differentiates them from expiry-triggered refreshes.

#### Scenario: Log message on 401-triggered refresh
- **WHEN** the service detects an upstream 401 and initiates a refresh
- **THEN** it logs a message containing "[codex] upstream 401 — refreshing token"
- **AND** includes the error code from the 401 response body if available

### Requirement: One retry maximum per request

The retry-on-401 mechanism SHALL attempt at most one refresh+retry cycle per incoming client request. No nested retries.

#### Scenario: Only one retry attempt
- **WHEN** the first upstream attempt returns 401
- **AND** refresh succeeds
- **AND** the retry attempt is made
- **THEN** regardless of the retry outcome, no further refresh or retry is attempted for this request

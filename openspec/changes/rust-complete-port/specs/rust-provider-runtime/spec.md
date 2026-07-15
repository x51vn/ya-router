## ADDED Requirements

### Requirement: Rust provider boundary is capability-oriented

The Rust runtime SHALL define an asynchronous provider abstraction covering provider identity, health, authentication, supported capabilities, model discovery, and generic proxy requests.

#### Scenario: Router holds providers behind one abstraction

- **WHEN** enabled providers are initialized
- **THEN** the router SHALL dispatch through provider trait objects rather than concrete provider types.

#### Scenario: Capability is checked before dispatch

- **WHEN** a request resolves to a provider that does not support the requested capability
- **THEN** the router SHALL return an explicit unsupported-capability error without sending an upstream request.

### Requirement: Provider parity follows the current Go inventory

The Rust runtime SHALL implement every provider and provider capability present in the Go runtime at implementation start and at production cutover.

#### Scenario: A provider is added to Go during the port

- **WHEN** the pre-cutover inventory finds a provider or capability not present in Rust
- **THEN** cutover SHALL remain blocked until Rust implements and tests equivalent behavior.

### Requirement: Codex transport and credential boundaries are preserved

The Rust Codex provider SHALL keep API-key and ChatGPT auth modes isolated. The official Codex credential store SHALL be read-only import data.

#### Scenario: API-key mode sends a request

- **WHEN** Codex is in API-key mode
- **THEN** the provider SHALL send the credential only to the OpenAI Platform endpoint.

#### Scenario: ChatGPT mode sends a request

- **WHEN** Codex uses ChatGPT OAuth credentials
- **THEN** the provider SHALL send those credentials only to the ChatGPT Codex backend.

#### Scenario: An account-pool entry is selected

- **WHEN** a named Codex account is selected
- **THEN** global import data SHALL NOT override that account's credentials.

#### Scenario: Authentication completes

- **WHEN** ya-router obtains or refreshes Codex credentials
- **THEN** it SHALL write only its protected config and SHALL NOT modify `~/.codex/auth.json` or `$CODEX_HOME/auth.json`.

### Requirement: Provider transport preserves response semantics

Provider implementations SHALL preserve upstream status codes, relevant response headers, non-streaming response bodies, and SSE event order.

#### Scenario: Upstream returns an error status

- **WHEN** an upstream returns `401`, `403`, `429`, or `5xx`
- **THEN** the client response and completion log SHALL retain the failure semantics and SHALL NOT report a successful `200`.

#### Scenario: Upstream streams SSE

- **WHEN** an upstream returns an SSE response
- **THEN** the provider SHALL forward events incrementally without buffering the complete response.

### Requirement: Incompatible requests fail explicitly

Providers and transforms SHALL reject fields or capabilities that cannot be represented safely. They SHALL NOT silently discard unsupported request data.

#### Scenario: A field has no safe upstream representation

- **WHEN** a client supplies such a field
- **THEN** the runtime SHALL return a client error before proxying.

### Requirement: Provider secrets remain confidential

Logs, health responses, errors, and CLI status output SHALL NOT contain raw access tokens, refresh tokens, API keys, device codes, or account IDs.

#### Scenario: Provider auth fails

- **WHEN** an authentication or refresh operation fails
- **THEN** diagnostics SHALL include only redacted metadata and typed failure information.

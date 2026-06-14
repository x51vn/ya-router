## ADDED Requirements

### Requirement: Codex responses requests enforce store false
The system SHALL send `store: false` on every native Codex responses upstream request, regardless of whether the client omitted `store` or supplied another value.

#### Scenario: Client omits store
- **WHEN** a client sends `POST /v1/responses` for a Codex-routed model without a `store` field
- **THEN** the proxy SHALL construct the upstream Codex request body with `store` set to `false`

#### Scenario: Client sets store true
- **WHEN** a client sends `POST /v1/responses` for a Codex-routed model with `store` set to `true`
- **THEN** the proxy SHALL override the upstream Codex request body so `store` is `false`

### Requirement: Store enforcement is regression tested
The system MUST include automated regression coverage for Codex responses request shaping so future adapter changes cannot remove the enforced `store: false` behavior unnoticed.

#### Scenario: Adapter test coverage
- **WHEN** automated tests build a native Codex responses request from an OpenAI-compatible payload
- **THEN** the tests SHALL assert that the resulting upstream JSON includes `store: false`

#### Scenario: Routed proxy test coverage
- **WHEN** automated tests exercise the Codex `/v1/responses` proxy path
- **THEN** the tests SHALL assert that the provider receives a request body whose `store` field is `false`

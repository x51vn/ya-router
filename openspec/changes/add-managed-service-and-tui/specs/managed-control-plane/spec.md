# Managed Service and TUI Specification

## Requirement: Daemon-owned state

The running daemon SHALL be the only writer of service configuration, provider credentials, operation state, and effective runtime state.

### Scenario: Client changes a provider

- **WHEN** an authorized client submits a provider change
- **THEN** the client SHALL call the control API
- **AND** SHALL NOT edit service files or provider credential stores directly.

### Scenario: Second daemon starts

- **WHEN** another daemon attempts to own the same state directory
- **THEN** startup SHALL fail with an actionable lock error
- **AND** SHALL NOT modify state.

## Requirement: Data and control planes are isolated

The service SHALL expose management operations on a listener and authorization policy separate from the OpenAI-compatible data API.

### Scenario: Data API credential calls control API

- **WHEN** a caller presents only `YA_ROUTER_API_KEY` to the control plane
- **THEN** access SHALL be denied.

### Scenario: Remote management is enabled

- **WHEN** a control listener binds to a non-loopback network address
- **THEN** TLS and an accepted remote identity mechanism SHALL be configured
- **AND** plaintext startup SHALL fail.

## Requirement: Runtime changes preserve in-flight requests

The service SHALL publish immutable runtime snapshots and SHALL replace provider instances without mutating an instance serving requests.

### Scenario: Provider configuration succeeds

- **WHEN** a validated replacement provider becomes ready
- **THEN** new requests SHALL use the new snapshot
- **AND** existing requests SHALL finish against their original snapshot
- **AND** the old provider SHALL drain for a bounded duration.

### Scenario: Provider replacement fails

- **WHEN** a replacement cannot be constructed or validated
- **THEN** the prior effective snapshot SHALL remain active.

## Requirement: Configuration mutations are revision-safe

Every configuration mutation SHALL validate an expected revision and SHALL be idempotent, auditable, durable, and rollback-capable.

### Scenario: Two clients update one revision

- **WHEN** the first update commits a new revision
- **AND** the second update still references the old revision
- **THEN** the second update SHALL return a conflict
- **AND** SHALL NOT overwrite the first update.

### Scenario: Process stops during persistence

- **WHEN** the service stops before a new configuration is durably committed
- **THEN** restart SHALL recover the previous last-known-good revision.

## Requirement: Secrets are write-only

Provider credentials SHALL be submitted only over an authenticated control transport and SHALL never be returned through control resources, logs, audit, health, events, or diagnostics.

### Scenario: API key is configured

- **WHEN** an operator submits an API key
- **THEN** the response SHALL contain only redacted configured/source metadata.

### Scenario: Environment credential has precedence

- **WHEN** a provider credential is owned by a higher-precedence environment or deployment source
- **THEN** the control API SHALL report it as read-only
- **AND** SHALL NOT claim a lower-precedence replacement is effective.

## Requirement: Long-running work is resumable

Device authentication and other long-running management work SHALL use operation resources independent from one client connection.

### Scenario: TUI exits during device authentication

- **WHEN** the TUI disconnects after an auth operation starts
- **THEN** the daemon SHALL continue until success, cancellation, failure, or expiry
- **AND** a client SHALL be able to reconnect by operation ID.

### Scenario: Daemon restarts during an unsafe operation

- **WHEN** safe resume is not supported
- **THEN** restart SHALL mark the operation failed or expired
- **AND** SHALL NOT install partial credentials.

## Requirement: Provider management is descriptor-driven

The control API SHALL describe provider capabilities, auth methods, account behavior, credential source, health, and model-catalog status without requiring provider-specific client protocols.

### Scenario: A new compiled-in provider is disabled

- **WHEN** the client lists providers
- **THEN** the provider SHALL appear with `disabled` state and its supported descriptors.

## Requirement: TUI and automation share one client contract

The OS client SHALL support an interactive TUI and non-interactive commands using the same typed control client.

### Scenario: No TTY is available

- **WHEN** a read command runs in automation
- **THEN** it SHALL support stable exit codes and structured JSON output.

### Scenario: Client exits

- **WHEN** the user exits the TUI
- **THEN** the daemon and active operations SHALL continue unless explicitly cancelled through the API.

## Requirement: Lifecycle managers retain process ownership

The control API SHALL NOT invoke Docker, systemd, a shell, arbitrary files, or arbitrary URL fetches.

### Scenario: A setting requires restart

- **WHEN** a validated setting cannot be safely applied in process
- **THEN** the service SHALL report `restart_required`
- **AND** SHALL NOT attempt to restart itself.

## Requirement: Production distributions are hardened

The project SHALL provide least-privilege systemd and Docker deployment artifacts plus signed/checksummed release evidence and compatibility tests.

### Scenario: Docker starts

- **WHEN** the production image runs
- **THEN** the daemon SHALL run non-root with dropped capabilities and supported read-only filesystem mounts.

### Scenario: Package upgrades

- **WHEN** the systemd package is upgraded or rolled back within the supported window
- **THEN** configuration and provider credentials SHALL remain recoverable.

## Requirement: Existing data-plane invariants remain blocking

The implementation SHALL preserve provider prefixes, auth-source separation, upstream status and SSE fidelity, retry safety, and `/v1/*` compatibility.

### Scenario: Management implementation changes

- **WHEN** any roadmap issue is merged
- **THEN** existing Go format, vet, race-test, build, and data-plane contract checks SHALL remain blocking.


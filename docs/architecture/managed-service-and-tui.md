# Managed ya-router Service and TUI Architecture

Status: proposed target architecture  
Audience: maintainers, contributors, operators, and security reviewers  
Last updated: 2026-07-15

## 1. Decision summary

ya-router will evolve from a single process with direct CLI-owned configuration into two distributable executables:

- `ya-routerd`: the long-running service and the only owner of runtime state, provider credentials, configuration writes, model caches, and provider lifecycle;
- `ya`: the operating-system client. Running `ya` without a subcommand opens the TUI; non-interactive subcommands provide scriptable control and JSON output.

The repository and product remain named `ya-router`. During migration, the existing `ya-router` binary and commands remain compatible wrappers until a versioned removal decision is accepted.

The service exposes two strictly separated planes:

- the **data plane** keeps the OpenAI-compatible `/v1/*` API and existing provider-routing behavior;
- the **control plane** exposes a versioned management API used by `ya`, future automation, and no untrusted model client.

The TUI never edits the service configuration file, reads provider secrets, calls the Docker socket, or invokes `systemctl` on behalf of the operator.

## 2. Context

The current Go runtime already has useful production foundations:

- a provider abstraction for Copilot, Codex, and Kilo Gateway;
- deterministic provider-prefixed routing;
- typed provider failures, upstream status preservation, bounded retry, and SSE passthrough;
- provider health, model discovery, config migration, atomic file replacement, and graceful HTTP shutdown;
- a static binary and Docker deployment path.

However, provider registration occurs only during process startup. Authentication commands load and write configuration outside the running service. The configuration has no revision or cross-process concurrency contract, and the same listener-level security policy protects data-plane requests. A remote TUI added directly to those paths would introduce competing writers, ambiguous authorization, and runtime/configuration drift.

This architecture makes the daemon the single source of truth before adding interactive management.

## 3. Goals

1. Operate a local or remote ya-router service safely from a terminal client.
2. List supported and configured providers, accounts, capabilities, health, credential source, and model-catalog freshness.
3. Enable or disable providers and accounts without editing files.
4. Run device-code, API-key, and anonymous authentication flows without exposing stored credentials to the client.
5. List, search, refresh, and filter provider models.
6. Validate and apply supported routing/configuration changes with revision conflict protection.
7. Apply safe changes without interrupting in-flight data-plane requests.
8. Support hardened systemd and Docker deployments from the same service contract.
9. Preserve existing data-plane routing, auth isolation, and compatibility invariants.
10. Provide an automation-friendly client and API in addition to the interactive TUI.

## 4. Non-goals for the first production release

- A browser UI.
- A provider plugin marketplace or runtime-loaded executable plugins.
- A clustered or multi-writer control plane.
- TUI access to the Docker daemon or systemd D-Bus.
- Self-updating binaries or self-restarting containers.
- Exporting provider credentials.
- Cross-provider billing fallback.
- Replacing the existing OpenAI-compatible data API.
- Removing the Go runtime before the separate Rust-port acceptance gates pass.

## 5. Naming and distribution

| Artifact | Purpose | Production name |
|---|---|---|
| Service executable | Data plane, control plane, providers, state | `ya-routerd` |
| Client executable | TUI and non-interactive control commands | `ya` |
| systemd unit | Linux service lifecycle | `ya-router.service` |
| Container image | Service-only runtime | `ya-router` |
| Repository/product | Source and documentation | `ya-router` |

`ya-services` is intentionally not used: the runtime is one service with multiple internal components, not a collection of independently deployed microservices.

## 6. Logical architecture

```text
 OpenAI-compatible clients
            |
            v
 +-------------------------+
 | Data API listener       |
 | /v1/* and /health/*     |
 +------------+------------+
              |
              v
 +------------+------------+       +----------------------+
 | Router and proxy core   +------>| Provider instances   |
 +------------+------------+       | Copilot/Codex/Kilo   |
              ^                    +-----------+----------+
              |                                ^
              | atomic runtime snapshot        |
 +------------+--------------------------------+----------+
 | Runtime manager                                        |
 | provider manager | config manager | operation manager  |
 | secret store     | model cache    | event/audit bus     |
 +------------+-------------------------------------------+
              ^
              |
 +------------+------------+       Unix socket by default
 | Control API listener    |       TLS + mTLS/OIDC remotely
 | /control/v1/*           |
 +------------+------------+
              ^
              |
 +------------+------------+
 | ya client               |
 | TUI + ctl + JSON output |
 +-------------------------+

 systemd or Docker owns start, stop, restart, and upgrade.
```

## 7. Component responsibilities

### 7.1 Data API

The data API preserves the existing public contract:

- `GET /v1/models`;
- `POST /v1/chat/completions`;
- `POST /v1/responses`;
- `POST /v1/embeddings`;
- `/health`, `/health/live`, `/health/ready`, and `/health/providers`.

It uses a read-only runtime snapshot. It never performs management authorization and never mutates desired configuration in response to model traffic.

### 7.2 Control API

The control API validates identity, role, request revision, and idempotency before invoking the runtime manager. It returns typed, redacted resources rather than exposing provider structs or configuration files.

It runs on a separate listener and uses a credential independent from `YA_ROUTER_API_KEY`.

### 7.3 Runtime manager

The runtime manager is the service composition root and the only writer of runtime state. It owns:

- the effective immutable configuration snapshot;
- provider construction and replacement;
- authentication and model-refresh operations;
- configuration validation and revision publication;
- lifecycle events, audit events, and restart-required state.

Global side effects such as setting a health registry during `NewProviderRegistry` construction must be removed. Tests and validation builds must be able to create isolated registries.

### 7.4 Provider manager

The current request-oriented `Provider` interface remains narrow. Management capabilities are modeled separately:

- `ProviderFactory`: validates provider configuration and constructs an instance;
- `ProviderDescriptor`: describes supported auth methods, capabilities, multi-account behavior, and configuration schema;
- `ProviderManager`: reconciles desired provider/account state to effective instances;
- `AuthController`: starts and resumes provider-specific authentication operations.

When a provider configuration changes, the manager builds and validates a replacement, atomically publishes a new runtime snapshot, and drains the old instance. In-place mutation of a provider serving requests is forbidden.

### 7.5 Operation manager

Device authentication, credential verification, model refresh, and configuration apply may outlive one HTTP request or TUI connection. They are represented as operations with:

- opaque operation ID;
- type and target;
- state, progress, creation/expiry timestamps, and owner;
- cancelability;
- redacted typed failure;
- optional result metadata;
- event sequence number for reconnect.

The first version supports `pending`, `running`, `waiting_for_user`, `succeeded`, `failed`, `cancelled`, and `expired`.

TUI disconnection does not cancel an operation. A client reconnects by operation ID or resumes the event stream with `Last-Event-ID`.

### 7.6 Config manager

The config manager distinguishes:

- **desired configuration**: the latest validated operator intent;
- **effective configuration**: the immutable snapshot currently serving requests;
- **restart-required configuration**: validated changes that cannot be applied safely in process.

The daemon is the sole config writer. Every revision includes a monotonic revision, content digest, timestamp, actor, and schema version. Mutation requires an expected revision; stale writes return `409 Conflict`.

File persistence must use a process lock, unique temporary file, restrictive permissions, file sync, atomic rename, parent-directory sync, and bounded last-known-good backups.

### 7.7 Secret store

Routing and non-secret configuration reference credentials but do not contain raw secret values in the target schema.

The secret-store abstraction supports:

- service-owned encrypted storage with a master key supplied separately;
- read-only environment or systemd/Docker credential sources;
- an external secret manager such as Vault in managed deployments.

An environment-owned secret is displayed as `read_only` and cannot be overwritten by the TUI while the higher-precedence source remains configured. Encryption is not claimed when the data key is stored beside the encrypted data.

Secret values are write-only through the control API. They are never returned, diffed, logged, audited, included in health data, or exposed in crash diagnostics.

### 7.8 Event and audit services

Operational events drive TUI updates. Audit records provide durable accountability. They are separate streams because high-frequency health changes do not all require durable retention.

Audit records include actor, role, action, target, result, request ID, old/new config revision, and timestamp. They exclude access tokens, refresh tokens, API keys, device codes, raw account IDs, and request/response prompt content.

## 8. Domain model

### 8.1 Provider state

Provider availability is not represented by one `connected` boolean.

```text
disabled
  -> enabled_unconfigured
  -> authenticating
  -> ready
  -> degraded
  -> error
```

Each provider response also reports independent facts:

- enabled state;
- supported and effective capabilities;
- auth methods;
- credential source and whether it is writable;
- account count and redacted account labels;
- model catalog status and age;
- last successful request/refresh metadata;
- retryable redacted error, if any.

### 8.2 Account state

Copilot and Codex multi-account behavior is first-class. An account has a stable internal ID, operator label, enabled state, credential state, priority, cooldown metadata, and last authentication timestamp. Raw upstream account identifiers are not returned.

Kilo initially exposes one logical account with anonymous or API-key authentication, while keeping the API shape compatible with future multi-account support.

### 8.3 Authentication methods

Provider descriptors may advertise:

- `device_code`;
- `api_key`;
- `manual_token_recovery`;
- `anonymous`.

The client renders controls from descriptors. It does not hard-code a separate protocol for each provider.

### 8.4 Model catalog state

A model result identifies provider, upstream ID, client-facing prefixed ID, capability, availability, allowlist result, discovery timestamp, and stale flag. Anonymous Kilo catalogs retain the existing free-only invariant.

## 9. Control API contract

### 9.1 Transport matrix

| Deployment | Default control transport | Authentication |
|---|---|---|
| systemd, local client | `/run/ya-router/control.sock` | socket owner/group, optional Linux peer credentials |
| Docker, same host | mounted Unix socket or loopback-only control port | socket permissions or short-lived client credential |
| Remote client | dedicated HTTPS endpoint | mTLS or OIDC bearer token |

Plain HTTP on a non-loopback control address is forbidden. CORS is disabled because the first-party client is not a browser.

### 9.2 Minimum resources

```text
GET    /control/v1/meta
GET    /control/v1/providers
GET    /control/v1/providers/{provider}
PATCH  /control/v1/providers/{provider}

GET    /control/v1/providers/{provider}/accounts
POST   /control/v1/providers/{provider}/accounts
PATCH  /control/v1/providers/{provider}/accounts/{account}
DELETE /control/v1/providers/{provider}/accounts/{account}

POST   /control/v1/auth-sessions
GET    /control/v1/auth-sessions/{operation}
DELETE /control/v1/auth-sessions/{operation}

GET    /control/v1/models
POST   /control/v1/providers/{provider}/models:refresh

GET    /control/v1/config
PATCH  /control/v1/config
POST   /control/v1/config:validate
POST   /control/v1/config:rollback

GET    /control/v1/operations
GET    /control/v1/operations/{operation}
GET    /control/v1/events
GET    /control/v1/audit
```

### 9.3 Error contract

Control errors are not OpenAI error payloads. They use a stable management schema:

```json
{
  "error": {
    "code": "config_revision_conflict",
    "message": "Configuration changed; reload before applying.",
    "retryable": false,
    "request_id": "req_...",
    "details": {
      "expected_revision": 11,
      "current_revision": 12
    }
  }
}
```

Messages and details are sanitized by the server. The TUI does not render arbitrary upstream error bodies.

### 9.4 Idempotency and concurrency

All mutating calls accept `Idempotency-Key`. Configuration mutations also require `If-Match` or an equivalent expected revision. A repeated request returns the original operation/result; a mismatched payload with the same key fails.

### 9.5 Version negotiation

`GET /control/v1/meta` reports service version, API versions, feature flags, minimum/maximum supported client versions, deployment mode, and restart-required state.

The compatibility target is current client plus one previous minor client version. Unsupported combinations fail before a mutation is attempted.

## 10. Authentication flows

### 10.1 Device code

1. The client creates an auth session for a provider/account.
2. The daemon requests a device code from the upstream.
3. The operation enters `waiting_for_user` with verification URI, user code, and expiry.
4. The TUI displays the data and may open a browser only after explicit user action.
5. The daemon polls and owns retry/expiry behavior even if the TUI exits.
6. The daemon stores the credential and publishes the new provider snapshot.
7. The operation returns redacted credential metadata, never the credential.

### 10.2 API key

The TUI uses masked input and sends the value only over an authenticated Unix socket or TLS connection. The server validates it when supported, stores it through `SecretStore`, clears request buffers on a best-effort basis, and returns only source and configured state.

Secret-bearing argv flags remain forbidden. Non-interactive import reads from stdin.

### 10.3 Anonymous access

Anonymous is an explicit auth method, not the absence of policy. The provider descriptor and TUI state show its limitations. Kilo anonymous mode remains free-only and retains the Auto Free data-handling warning.

## 11. Authorization and trust model

The control plane defines three roles:

| Role | Allowed actions |
|---|---|
| `viewer` | Read redacted service, provider, model, operation, config, and audit data |
| `operator` | Viewer actions plus auth, refresh, and account/provider enable/disable |
| `admin` | Operator actions plus routing/config mutation, rollback, and credential replacement |

Local socket permissions provide the first deployment boundary. Remote identities come from mTLS subjects or OIDC claims and map to roles. `YA_ROUTER_API_KEY` authorizes data-plane use only and never authorizes control-plane access.

The first release does not expose shutdown, restart, Docker, systemd, shell, arbitrary file, or arbitrary URL-fetch operations.

## 12. Hot reload and request safety

Safe provider, account, allowlist, credential, and routing changes should apply in process. Listener address, TLS listener, storage backend, and some process-level settings return `restart_required=true`.

The effective runtime is published as an immutable snapshot. Data requests retain the snapshot they started with. Old providers drain for a bounded period before close. A failed replacement leaves the previous effective snapshot active and records a failed operation.

Management actions must not stop the shared proxy worker pool or interrupt unrelated provider traffic.

## 13. TUI and non-interactive client

### 13.1 Screens

1. Connection profiles and compatibility status.
2. Service overview and readiness.
3. Provider and account list.
4. Provider detail and authentication operations.
5. Model search, filter, catalog age, and refresh.
6. Routing/default model/allowlist validation and diff.
7. Running and recent operations.
8. Sanitized event and audit views.

### 13.2 Interaction requirements

- Complete keyboard operation; mouse is optional.
- Responsive layouts for small terminals and resize events.
- Light, dark, and monochrome modes; status is never color-only.
- SSH, tmux, common Linux terminals, macOS Terminal/iTerm, and Windows Terminal coverage.
- Masked secret input without terminal history or application logging.
- Explicit confirmation for credential replacement, account deletion, disable, rollback, and disruptive changes.
- Automatic reconnect with bounded exponential backoff and clear offline state.
- Exiting the client never stops the daemon or cancels an operation by default.
- Correlation/request ID is shown for failures.

### 13.3 Automation

The same client library powers TUI commands and non-interactive commands. Scriptable commands support stable exit codes, `--json`, connection profile selection, timeouts, and stdin secret import.

Bubble Tea v2 with Bubbles and Lip Gloss is the preferred initial Go stack. Dependencies are pinned and validated in terminal integration tests before acceptance.

## 14. Deployment architecture

### 14.1 systemd

The supported Linux package installs the daemon, client, unit, default config template, and shell completion. The service runs under a dedicated user and group with:

- `StateDirectory=ya-router`;
- `RuntimeDirectory=ya-router`;
- `UMask=0077`;
- `NoNewPrivileges=true`;
- `ProtectSystem=strict`;
- `ProtectHome=true`;
- private temporary/device settings where compatible;
- empty capability bounding set;
- restricted address families;
- restart-on-failure and bounded graceful shutdown.

Logs use journald or structured stdout/stderr. Secret material is supplied through the selected secret backend, not embedded in the unit.

### 14.2 Docker

The production container runs the daemon only. It runs as non-root, uses a read-only root filesystem, drops all capabilities, enables no-new-privileges, mounts state and secrets separately, uses tmpfs where needed, and exposes distinct liveness/readiness checks.

The build produces multi-architecture images, pinned dependency/base inputs, SBOM, checksums, provenance/signature evidence, and reproducible release metadata. Docker or Compose owns restart and upgrade.

### 14.3 Remote management

Remote management is opt-in. The operator explicitly configures the control listener, server certificate, trust roots, identity provider or client certificates, and role mapping. Control and data listener addresses may be placed behind different network policies.

## 15. Observability

The service emits structured, redacted logs and metrics for:

- data/control request counts, status, and latency;
- provider readiness, refresh, cooldown, and catalog age;
- operation state/duration/failure kind;
- config revision/apply/rollback;
- audit persistence failures;
- runtime snapshot replacement and drain.

Prompts, completions, tokens, API keys, OAuth tokens, device codes, and raw account IDs are excluded. Control request IDs propagate through operation and audit records.

## 16. Reliability and recovery

- A process lock prevents two daemons from owning one state directory.
- The service loads the last-known-good config and model snapshot before background refresh.
- An interrupted config write cannot replace the prior valid revision.
- Incomplete auth operations become failed/expired on restart unless the provider explicitly supports safe resume.
- Audit and operation retention are bounded and configurable.
- Corrupt state is quarantined with a clear startup error; the service never silently creates a fresh secret store over unreadable state.
- Graceful shutdown drains data requests and stops accepting control mutations before exit.

## 17. Backward compatibility and migration

1. Refactor packages without changing the current `ya-router` data API or config behavior.
2. Add read-only control APIs and client while existing CLI commands continue to work.
3. Move auth mutations behind the daemon and prevent direct config writes while the daemon lock is held.
4. Introduce secret references with a versioned, rollback-capable migration.
5. Add compatibility wrappers from existing commands to the local control API.
6. Introduce `ya-routerd` and `ya` release artifacts.
7. Remove legacy mutation paths only in a separately accepted breaking release.

The historical config path remains readable during migration. The official Codex store remains read-only import data throughout.

## 18. Testing and acceptance gates

The feature is not production-ready until all of the following pass:

- unit and race tests for runtime snapshot publication and provider drain;
- API contract, authz, revision-conflict, idempotency, redaction, and fuzz tests;
- mocked Copilot, Codex, and Kilo authentication operations;
- TUI reducer/golden tests and PTY tests across supported terminals;
- daemon restart during config write, auth flow, and model refresh;
- concurrent data-plane load while providers/configuration are changed;
- N-1 client compatibility tests;
- systemd sandbox analysis and package install/upgrade/rollback tests;
- hardened Docker/Compose integration, non-root, read-only filesystem, and signal tests;
- secret scanning, dependency scanning, SBOM, checksums, and release signing;
- manual redacted end-to-end validation with real provider accounts.

## 19. Key architecture decisions

| ID | Decision | Rationale |
|---|---|---|
| AD-01 | Daemon is the only state writer | Prevents TUI/CLI/service write races and runtime drift |
| AD-02 | Separate data and control listeners | Keeps model-client credentials and admin authority isolated |
| AD-03 | REST/JSON plus SSE | Simple to inspect, automate, and extend without a WebSocket/gRPC-only client |
| AD-04 | Immutable runtime snapshots | Allows safe hot reload without mutating providers serving requests |
| AD-05 | Provider descriptors drive clients | New providers do not require hard-coded TUI protocols |
| AD-06 | Async operation resources | Authentication and refresh survive client disconnects |
| AD-07 | JSON config may remain initially | Preserves simplicity if one daemon writer, revisioning, locking, and recovery are added |
| AD-08 | No lifecycle-manager access from TUI | Avoids root-equivalent Docker/systemd permissions |
| AD-09 | One client supports TUI and automation | Prevents the TUI from becoming an untestable administration-only surface |
| AD-10 | Go remains production reference during work | Avoids combining a runtime port with a control-plane redesign |

## 20. References

- Bubble Tea, Bubbles, and Lip Gloss: <https://github.com/charmbracelet/bubbletea>, <https://github.com/charmbracelet/bubbles>, <https://github.com/charmbracelet/lipgloss>
- Docker build guidance: <https://docs.docker.com/build/building/best-practices/>
- Kilo provider implementation: <https://github.com/duvu/ya-router/pull/5>


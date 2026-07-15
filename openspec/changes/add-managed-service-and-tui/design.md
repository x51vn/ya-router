# Design: Managed service and TUI control plane

## Boundaries

The daemon owns providers, credentials, configuration writes, operations, model caches, and effective runtime state. The client is stateless apart from connection profiles and client-side credential references.

The existing OpenAI-compatible endpoints remain on the data-plane listener. A separate control listener exposes `/control/v1/*` and rejects data-plane credentials.

## Runtime model

An immutable runtime snapshot contains routing and provider instances used by data-plane requests. Management changes are validated by the runtime manager, built as replacements, atomically published, and drained. A failure leaves the previous snapshot effective.

Provider request behavior remains in the narrow `Provider` interface. Provider factory, descriptor, lifecycle, and auth control are separate management abstractions.

## State model

The daemon is the only writer. Desired, effective, and restart-required configurations are distinguished. Every mutation is revision-checked, idempotent, audited, durable, and rollback-capable.

Raw credentials are removed from ordinary target configuration and referenced through a secret-store abstraction. Environment/systemd/Docker sources may be read-only and retain defined precedence.

## Control protocol

REST/JSON is used for resources and commands; SSE is used for resumable one-way events. Long-running device authentication and refresh work is represented by operation resources. Typed errors contain a stable code, retryability, request ID, and sanitized details.

Local systemd management defaults to a Unix socket. Remote management is opt-in and requires TLS with mTLS or OIDC identity. Viewer, operator, and admin roles are enforced separately from `YA_ROUTER_API_KEY`.

## Client

One client executable provides both a Bubble Tea TUI and non-interactive commands with stable exit codes and JSON output. Provider descriptors drive generic screens and auth controls so new compiled-in providers do not require a new client protocol.

## Lifecycle ownership

systemd or Docker owns process start, stop, restart, and upgrade. The control API may reload safe runtime state and report `restart_required`; it does not execute lifecycle-manager commands.

## Compatibility

The Go runtime remains the production reference. Existing `/v1/*`, config migration, provider prefixes, auth-source precedence, and historical config path remain compatible until separately versioned migrations and removal gates are accepted.

## Detailed contract

The normative architecture and acceptance details live in:

- `docs/architecture/managed-service-and-tui.md`;
- `docs/roadmaps/managed-service-and-tui-roadmap.md`;
- `specs/managed-control-plane/spec.md`.


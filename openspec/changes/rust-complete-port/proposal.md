## Why

`ya-router` currently has one production implementation: the hardened Go runtime under `src/`. No Rust workspace or Rust runtime is present in the repository. The earlier version of this change incorrectly described a partially completed Rust port and carried forward names and behaviors that conflict with the current `ya-router` contracts.

This change therefore defines a future Rust port from a clean starting point. The port is additive until it proves behavioral parity with Go. It must not weaken routing determinism, credential isolation, error semantics, protocol fidelity, or deployment safeguards.

## What Changes

- Create a new Rust implementation under `rust/` without changing the Go build or deployment path.
- Use an async HTTP stack suitable for concurrent streaming requests.
- Reproduce every public endpoint: model listing, Chat Completions, native Responses, embeddings, and all health endpoints.
- Reproduce the provider registry and every provider present in the Go runtime at the start of implementation and again at cutover.
- Keep the official Codex credential store read-only and preserve account-pool credential ownership.
- Preserve upstream HTTP status, selected headers, non-streaming bodies, and SSE event fidelity.
- Reject unsupported request fields explicitly instead of silently removing them.
- Build executable parity tests and representative benchmarks before any production cutover.
- Keep the binary, image, and service name `ya-router`.
- Retire Go only in a separate, explicit cutover after parity, security, performance, packaging, and rollback gates pass.

## Capabilities

### New Capabilities

- `rust-async-runtime`: additive Rust server and upstream HTTP transport.
- `rust-provider-runtime`: provider abstraction and provider implementations matching Go behavior.
- `rust-parity-validation`: shared fixtures and side-by-side contract tests.
- `rust-production-packaging`: non-production Rust image and CI validation before cutover.
- `go-retirement`: separately gated removal of Go after Rust is production-proven.

### Modified Capabilities

- `rust-runtime-port`: starts from an empty Rust workspace rather than a pre-existing skeleton.
- `rust-port-validation`: all security and protocol invariants in `AGENTS.md` are blocking acceptance criteria.

## Impact

- New `rust/` workspace and Rust-specific test fixtures.
- Additive Rust validation in `Makefile` and CI; existing Go validation remains blocking.
- Optional `Dockerfile.rust` or equivalent non-production image during the transition.
- No config migration, auth-store migration, public endpoint change, provider removal, binary rename, or production deployment change is allowed before cutover.
- `src/`, `go.mod`, the Go image, and the Go deployment path remain authoritative until every cutover gate passes.

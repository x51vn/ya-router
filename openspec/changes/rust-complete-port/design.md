## Context

The repository currently contains only the production Go runtime. It already enforces deterministic provider routing, strict Codex transport boundaries, read-only import from the official Codex credential store, typed provider errors, accurate upstream status reporting, native Responses handling, SSE forwarding, and safe server exposure.

A Rust port may improve maintainability or runtime characteristics, but language choice alone is not a reason to accept behavioral drift. The Go implementation and the contracts documented in `AGENTS.md` and the parity inventory are the reference until an explicit cutover.

## Goals / Non-Goals

**Goals:**

- Create an additive async Rust implementation from scratch.
- Match all public HTTP, CLI, config, provider, auth, routing, retry, and error contracts.
- Preserve byte-level streaming behavior and upstream status semantics.
- Include every provider available in Go at cutover, including providers added after this proposal.
- Prove parity and operational readiness before changing production.
- Keep the product name and executable name `ya-router`.

**Non-Goals:**

- Changing provider entitlements, billing behavior, or fallback policy.
- Writing to or migrating the official Codex auth store.
- Silently dropping unsupported request fields.
- Renaming the service or moving the compatibility config directory.
- Removing Go incrementally or using the Rust implementation in production before acceptance gates pass.

## Decisions

### 1. Go remains the reference implementation

Before Rust code is added, record the reference Go commit and refresh the parity inventory. At cutover, refresh the inventory again so providers and capabilities added during the port are not omitted.

### 2. Rust starts as an additive workspace

Create `rust/` as a separate crate. Go remains the default build, image, and deployment. Rust-specific CI is additive and cannot make Go validation non-blocking.

### 3. Use an async HTTP stack

Use Tokio with Axum/Hyper and Reqwest, or an equivalently reviewed stack, for concurrent HTTP serving and upstream streaming. Pin dependency versions and review the lockfile before merge.

### 4. Keep a capability-oriented provider boundary

The Rust provider abstraction must expose provider identity, health, supported capabilities, model discovery, authentication, and generic request proxying. Capabilities include `chat`, `responses`, and `embeddings`; provider implementations decide which are available for each auth mode.

This avoids hard-coding chat and embeddings as separate trait methods and keeps future providers inside the same registry contract.

### 5. Preserve transport fidelity

Streaming responses pass through incrementally without full buffering. Non-streaming responses preserve the upstream status, relevant headers, and body. A forwarded `401`, `403`, or `429` must never become a successful `200` in either the response or completion logs.

### 6. Preserve Codex credential boundaries

The official Codex store at `~/.codex/auth.json` or `$CODEX_HOME/auth.json` is read-only import data. `ya-router` stores credentials it owns only in its own protected config. A selected account-pool entry owns its credentials and cannot be overridden by global import data.

API-key mode sends credentials only to OpenAI Platform endpoints. ChatGPT OAuth modes send credentials only to the ChatGPT Codex backend. Secret input uses environment variables or stdin, never command-line arguments.

### 7. Fail explicitly on incompatible requests

Request transforms may translate fields only where the target protocol has a defined representation. Unsupported fields return a client error. Native Responses requests bypass Chat Completions conversion, and unknown native Responses events pass through unchanged.

### 8. Keep routing deterministic

Rust preserves the exact order: explicit `routing.model_map`, authoritative provider prefix, provider catalog discovery, then default provider only when the request omitted a model. A prefixed request never falls through to another provider. Cross-provider billing fallback remains forbidden without a separate accepted specification.

### 9. Cut over once, after blocking gates

Rust becomes production only after contract parity, mocked provider/auth tests, SSE tests, security review, benchmarks, container validation, deployment rehearsal, and rollback rehearsal pass. Go retirement is one explicit change after those gates, not an incremental deletion.

## Risks / Trade-offs

- **Dependency risk:** the Rust HTTP stack adds supply-chain surface. Mitigate with pinned versions, lockfile review, and dependency scanning.
- **Auth regression risk:** OAuth and account pools are security-sensitive. Use mock identity/upstream servers in CI and keep real-account checks manual and redacted.
- **Protocol drift:** SSE and Responses adapters are easy to approximate incorrectly. Use shared fixtures and byte/event-level comparisons.
- **Moving target:** Go may gain providers while Rust is being built. Refresh provider parity at implementation start and cutover.
- **Cutover risk:** removing Go reduces rollback options. Keep the last Go image and a documented rollback until Rust has completed a production observation period.

## Migration Plan

1. Freeze the Go reference commit and refresh the parity inventory.
2. Create the additive Rust workspace and validation targets.
3. Implement server, transport, provider registry, routing, auth, and transforms.
4. Run shared contract tests against Go and Rust with mock upstreams.
5. Add benchmark and container evidence.
6. Reconcile the provider inventory with the then-current Go runtime.
7. Rehearse deployment and rollback without changing production.
8. Execute a separately reviewed cutover.
9. Retire Go only after the Rust production observation gate passes.

## Open Questions

- The Rust dependency versions and minimum supported Rust toolchain must be chosen when implementation begins.
- The benchmark thresholds must be derived from measured Go baselines rather than assumed in advance.
- The production observation period and rollback owner must be defined before cutover.

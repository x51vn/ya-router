## 1. Freeze the reference contract

- [ ] 1.1 Record the Go reference commit used by the port.
- [ ] 1.2 Refresh `openspec/changes/port-project-to-rust/inventory.md` against code and tests.
- [ ] 1.3 Confirm the provider list, capabilities, prefixes, config version, CLI commands, endpoints, and health payloads.
- [ ] 1.4 Convert the routing, auth, security, retry, Responses, SSE, and error invariants in `AGENTS.md` into shared test fixtures.

## 2. Create the additive Rust workspace

- [ ] 2.1 Create `rust/` from scratch with a pinned toolchain and reviewed lockfile.
- [ ] 2.2 Add Tokio, Axum/Hyper, Reqwest, and only the minimum supporting dependencies.
- [ ] 2.3 Add `rust-fmt`, `rust-test`, and `rust-build` targets without changing the default Go targets.
- [ ] 2.4 Add Rust CI as an additive job; keep all Go checks blocking.

## 3. Implement the HTTP and transport layer

- [ ] 3.1 Implement `/health`, `/health/live`, `/health/ready`, and `/health/providers` with redacted parity payloads.
- [ ] 3.2 Implement `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and `/v1/embeddings`.
- [ ] 3.3 Stream SSE bodies incrementally and preserve event ordering and flushing.
- [ ] 3.4 Preserve upstream status, relevant headers, and bodies for non-streaming responses.
- [ ] 3.5 Add request-size, timeout, cancellation, and client-disconnect tests.

## 4. Implement the provider registry and providers

- [ ] 4.1 Define a capability-oriented provider abstraction for identity, health, auth, models, and generic proxy requests.
- [ ] 4.2 Implement every provider present in the reference Go inventory.
- [ ] 4.3 Implement Copilot device-flow auth, refresh, model discovery, chat, and embeddings parity.
- [ ] 4.4 Implement Codex API-key and ChatGPT modes with strict endpoint separation.
- [ ] 4.5 Treat the official Codex store as read-only import data; store owned credentials only in the protected ya-router config.
- [ ] 4.6 Preserve account-pool credential ownership, cooldown, bounded failover, and one-refresh/one-retry behavior.
- [ ] 4.7 Map provider failures to the same typed error kinds and HTTP statuses as Go.
- [ ] 4.8 Add redaction tests proving tokens, keys, device codes, and raw account IDs never appear in logs or health output.

## 5. Implement routing and protocol transforms

- [ ] 5.1 Preserve routing order: model map, provider prefix, catalog discovery, then omitted-model default.
- [ ] 5.2 Preserve authoritative `github/` and `codex/` prefixes and add any providers introduced in Go before cutover.
- [ ] 5.3 Reject unknown explicit models, ambiguous bare models, unsupported capabilities, and prefixed fallthrough.
- [ ] 5.4 Translate Chat Completions structured output to Responses only when representable.
- [ ] 5.5 Return a client error for unsupported fields; do not silently strip them.
- [ ] 5.6 Keep native Responses requests and unknown native events on the native pass-through path.
- [ ] 5.7 Require `Idempotency-Key` before retrying an unsafe POST after uncertain delivery.

## 6. Implement CLI and configuration parity

- [ ] 6.1 Preserve `help`, `auth`, `run|start`, `migrate-config`, `models`, `config`, `status`, `refresh`, and `version`.
- [ ] 6.2 Preserve config version migration, atomic writes, directory mode, file mode `0600`, and the compatibility config path.
- [ ] 6.3 Read secrets from environment variables or stdin, never secret-bearing argv flags.
- [ ] 6.4 Keep the binary and service name `ya-router`.

## 7. Build parity and security evidence

- [ ] 7.1 Start Go and Rust against the same mock provider/auth servers.
- [ ] 7.2 Compare routing decisions, model lists, status codes, headers, JSON bodies, SSE events, and error payloads.
- [ ] 7.3 Cover Chat Completions, native Responses, embeddings, structured output, tools, rate limits, auth refresh, and account failover.
- [ ] 7.4 Run race/concurrency testing and dependency/security scanning.
- [ ] 7.5 Keep real-account end-to-end checks manual, redacted, and outside normal CI artifacts.

## 8. Benchmark and package

- [ ] 8.1 Measure the Go baseline for cold start, `/v1/models`, non-streaming chat, and concurrent SSE.
- [ ] 8.2 Define acceptance thresholds from the measured baseline.
- [ ] 8.3 Run equivalent Rust benchmarks and record reproducible evidence.
- [ ] 8.4 Build a non-production Rust image named `ya-router` and verify it under the existing container contract.

## 9. Reconcile before cutover

- [ ] 9.1 Refresh the provider and endpoint inventory against the latest Go `main`.
- [ ] 9.2 Implement and test any provider or contract added while the port was in progress.
- [ ] 9.3 Re-run all parity, security, benchmark, and container gates.
- [ ] 9.4 Document deployment, observability, rollback commands, owner, and production observation period.

## 10. Execute the separately reviewed cutover

- [ ] 10.1 Deploy Rust without deleting the Go source or last known-good Go image.
- [ ] 10.2 Complete the production observation period with no parity or security regression.
- [ ] 10.3 Switch the default build/deployment to Rust in one reviewed change.
- [ ] 10.4 Remove Go-only source and tooling only after rollback and observation gates pass.
- [ ] 10.5 Update `README.md`, `AGENTS.md`, configuration docs, and release notes to describe the final runtime.

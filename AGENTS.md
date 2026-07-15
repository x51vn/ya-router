# AGENTS.md

Compact orientation for development agents working in this repository.

## What this is

`ya-router` is a single-binary Go service exposing:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`
- `/health`, `/health/ready`, and `/health/providers`

It routes to GitHub Copilot, ChatGPT-backed Codex, OpenAI Platform API-key mode, or Kilo AI Gateway.

## Layout

- Provider implementations and the compatibility service live in the importable `src` package.
- Executable roots are `cmd/ya-router` (compatibility), `cmd/ya-routerd` (service), and `cmd/ya` (client foundation).
- Shared contracts live under `internal/api`, `internal/config`, `internal/provider`, `internal/proxy`, `internal/routing`, and `internal/runtime`.
- `internal/runtime.Manager` atomically publishes immutable snapshots. Every data-plane handler must acquire one lease at request start and release it only after the response finishes.
- `internal/provider.Manager` owns factory/descriptor registration, provider/account reconciliation, replacement/removal, bounded drain, lifecycle events, and the independent health registry. Never mutate a provider already present in a published snapshot.
- Build and test the full module with `./...`; the default `make build` still emits `ya-router`.
- `openspec/` records non-trivial changes and validation evidence.
- `docs/architecture/managed-service-and-tui.md` and `docs/roadmaps/managed-service-and-tui-roadmap.md` define the target daemon/control-plane/TUI direction; they do not override current runtime behavior until their ordered issues are implemented and accepted.
- Dated documents under `docs/` are historical decision records; current runtime code and accepted OpenSpec requirements take precedence when old request cards conflict.
- No Rust workspace currently exists. Any future Rust port starts as an additive implementation, keeps Go deployable until parity gates pass, and must consume every hardened routing, auth, security, and protocol contract before cutover.

## Exact validation

```bash
make fmt-check
make vet
make test
make build
# or
make check
```

Before claiming container readiness:

```bash
docker build -t ya-router:check .
```

CI runs formatting verification, `go vet ./...`, `go test -race -count=1 ./...`, builds all three binaries, and builds the compatibility container. Test failures are blocking.

## Routing invariants

1. `routing.model_map` is evaluated first.
2. `github/`, `codex/`, and `kilo/` prefixes are authoritative.
3. Provider catalogs are checked next.
4. The configured default provider is used only when the request omitted a model.
5. Explicit unknown models fail and do not implicitly rotate to or select a Copilot model.
6. A prefixed request never falls through to another provider.
7. Cross-provider billing fallback is forbidden unless an explicit future specification allows it.

Do not reintroduce a pre-router Copilot fast path.

## Runtime lifecycle invariants

- Build and validate every replacement before publication; a failed replacement must leave the effective snapshot unchanged.
- New requests use the newly published snapshot while existing requests finish against the snapshot lease they acquired.
- Provider close happens only after every pre-publication snapshot that might reference it has drained.
- A drain timeout is observable and returns control, but cleanup continues in the background; never force-close an instance still reachable by an in-flight request.
- Lifecycle events use stable reason codes and must not include arbitrary provider errors or secret values.
- Registered descriptors remain listable while a provider is disabled; effective capabilities remain separate from supported descriptor capabilities.

## Codex auth and transport invariants

- `api_key` mode uses `api.openai.com` only.
- `chatgpt`, `device_code`, and `chatgpt_device_auth` modes use the ChatGPT Codex backend only.
- ChatGPT OAuth tokens must never be sent to Platform chat, Responses, or embeddings endpoints.
- ChatGPT mode supports chat and native Responses; embeddings require API-key mode.
- The official Codex auth store is read-only import data. Never write, truncate, migrate, or normalize `~/.codex/auth.json`.
- Account-pool entries own their credentials. A global official-store token must not override a selected account.
- A ChatGPT `401` permits one refresh and one retry only.
- OAuth refresh retries only transient network failures, `429`, and `5xx`.
- Never log access tokens, refresh tokens, ID tokens, API keys, device codes, or raw account IDs.

`auth codex` performs the native device flow. It does not shell out to the Codex CLI. The OAuth client ID may be overridden through `YA_ROUTER_CODEX_OAUTH_CLIENT_ID`.

## Server security invariants

- Default listen address is `127.0.0.1`.
- Non-loopback binding requires `YA_ROUTER_API_KEY`.
- CORS is disabled unless `YA_ROUTER_CORS_ALLOWED_ORIGINS` is configured.
- `/health*` endpoints expose only redacted metadata.
- Secret CLI input uses stdin or environment variables; do not add secret-bearing argv flags.

## Kilo Gateway invariants

- The default upstream is `https://api.kilo.ai/api/gateway`.
- `KILO_API_KEY` takes precedence over a key imported into ya-router config.
- Anonymous mode exposes and accepts only free model IDs; it must not make paid requests implicitly.
- The inbound ya-router `Authorization` header is never forwarded to Kilo; provider credentials are server-owned.
- Kilo upstream status codes and SSE events pass through unchanged.
- Kilo does not participate in implicit cross-provider fallback.
- Auto Free may use providers with prompt/output logging or training policies; documentation must retain the data-handling warning.

## Protocol invariants

- Chat Completions `response_format` must be translated to Responses `text.format`.
- Unsupported fields fail explicitly; they are not silently dropped.
- Native `/v1/responses` requests bypass Chat Completions response translation.
- SSE error, failed, or incomplete events return an error and must not be logged as successful completion.
- Unknown native Responses events are passed through unchanged on the native endpoint.
- Unsafe POST retry requires an `Idempotency-Key` after uncertain delivery.

## Compatibility

- The current config path remains `~/.local/share/github-copilot-svcs/config.json` for migration safety.
- The build and container binary are named `ya-router`.
- Existing single-account auth and config-version migration remain supported.
- Do not delete legacy fields until a versioned migration and rollback path exist.

## Deployment

The manual release workflow validates before publishing or deploying. It reads credentials from GitHub Secrets and requires a pinned `known_hosts` payload. Do not restore `StrictHostKeyChecking=no`, host-environment secret scraping, or non-blocking tests.

## Style

- Keep provider-specific auth, request construction, and retry policy inside provider implementations.
- Use typed `ProviderError` values for operational failures.
- Keep tests alongside sources and use table-driven tests where practical.
- Do not add dependencies casually; the Go runtime is currently stdlib-only.

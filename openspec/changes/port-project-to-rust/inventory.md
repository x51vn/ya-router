# Go Runtime Parity Inventory

## Current Runtime Status

- The only runtime currently present is Go under `src/`.
- The production binary, image, and service name are `ya-router`.
- No `rust/` workspace currently exists.
- Go remains the reference implementation until an explicit, gated cutover.

## External HTTP Contract

- `GET /v1/models`: merged, provider-prefixed catalog plus visible `routing.model_map` entries.
- `POST /v1/chat/completions`: OpenAI-compatible Chat Completions surface.
- `POST /v1/responses`: native Responses surface; native events are not converted to Chat Completions chunks.
- `POST /v1/embeddings`: embeddings through providers/auth modes that support them.
- `GET /health` and `GET /health/live`: process liveness.
- `GET /health/ready`: readiness; unavailable providers can make it return `503`.
- `GET /health/providers`: redacted provider health and capabilities.

## CLI Contract

The dispatcher in `src/main.go` exposes:

- `help`
- `auth [copilot|codex] [--account <label>]`
- `run|start [--config-migrate merge|none|override]`
- `migrate-config --mode merge|override`
- `models [--provider <provider>] [--refresh]`
- `config`
- `status`
- `refresh [--provider <provider>]`
- `version`

Secrets are accepted through environment variables or stdin flags, not raw command-line values.

## Runtime and Credential Paths

- Compatibility config: `~/.local/share/github-copilot-svcs/config.json` or `YA_ROUTER_CONFIG_PATH`.
- Config writes are atomic; secret-bearing config uses file mode `0600`.
- Official Codex store: `~/.codex/auth.json` or `$CODEX_HOME/auth.json`.
- The official Codex store is read-only import data and is never rewritten by ya-router.
- Account-pool entries own their credentials; global import data cannot override a selected account.

## Routing Contract

1. Exact `routing.model_map` entry.
2. Explicit authoritative provider prefix.
3. Provider catalog discovery.
4. Configured default provider only when the request omitted a model.

Current prefixes:

- `github/` → GitHub Copilot.
- `codex/` → OpenAI Codex.
- `kilo/` → Kilo AI Gateway.

Unknown explicit models fail. Ambiguous bare model names fail. A prefixed request never falls through to another provider. Cross-provider billing fallback is forbidden without a separate accepted specification.

## Capabilities

The shared capability vocabulary is:

- `chat`
- `responses`
- `embeddings`

Providers expose only the capabilities their current auth mode can safely serve.

### GitHub Copilot

- Device-flow authentication and provider-owned refresh.
- Provider runtime: `src/copilot_provider.go`.
- Deterministic routing to the resolved upstream model.
- Chat and embeddings behavior are provider-owned.

### Codex

- Device auth and refresh: `src/codex_auth.go`.
- Provider runtime: `src/codex_provider.go`.
- API-key mode uses OpenAI Platform endpoints.
- ChatGPT OAuth modes use the ChatGPT Codex Responses backend.
- ChatGPT mode supports chat and native Responses.
- Embeddings require API-key mode.
- A ChatGPT `401` permits at most one refresh and one request retry.

### Kilo Gateway

- Provider runtime: `src/kilo_provider.go`.
- Default backend: `https://api.kilo.ai/api/gateway`.
- Dynamic model discovery through `GET /models`.
- Chat Completions and native Responses passthrough; embeddings are not advertised.
- `KILO_API_KEY` is optional for free anonymous models and takes precedence over config import.
- Anonymous mode exposes and accepts only free model IDs, including `kilo-auto/free`.
- Inbound router credentials are never forwarded to Kilo.
- Kilo status codes and SSE events pass through unchanged.
- Auto Free carries an explicit warning against confidential, personal, or regulated data.

## Protocol and Error Contract

- Chat Completions `response_format` is translated to Responses `text.format` only when representable.
- Unsupported fields fail explicitly; they are not silently removed.
- Native Responses requests bypass Chat Completions response conversion.
- Unknown native Responses events pass through unchanged.
- SSE error, failed, or incomplete events remain failures.
- Upstream failure status is preserved; `401`, `403`, and `429` are not logged as successful `200` requests.
- Unsafe POST retry after uncertain delivery requires `Idempotency-Key`.
- Provider failures use typed error kinds for invalid request, auth, entitlement, rate limit, unsupported capability, unavailable provider, and transport.

## Server Security Contract

- Default bind address is `127.0.0.1`.
- Non-loopback binding requires `YA_ROUTER_API_KEY`.
- CORS is disabled unless explicit origins are configured.
- Health endpoints expose redacted metadata only.
- Tokens, API keys, device codes, and raw account IDs never appear in logs.

## Build and Validation Contract

- Module target: `./src`.
- Binary: `ya-router`.
- Required checks: formatting, `go vet`, race-enabled tests, and production build.
- Current CI toolchain: Go 1.22.
- Go validation remains blocking throughout any additive Rust work.

## Parity-Relevant Tests

- `src/integration_test.go`: endpoints, merged model catalog, routing, auth-source and compatibility behavior.
- `src/proxy_test.go` and `src/proxy_freepool_regression_test.go`: dispatch, status, retry, and free-pool regressions.
- `src/responses_adapter_test.go`: Chat Completions/Responses conversion and streaming events.
- `src/hardening_test.go`: security, auth, routing, and transport boundaries.
- `src/kilo_provider_test.go`: Kilo catalog filtering, credential isolation, status/SSE passthrough, and base-URL policy.
- Provider-specific and prefix tests alongside production sources.

## Provider Drift Rule

Refresh this inventory twice:

1. immediately before Rust implementation starts;
2. immediately before production cutover.

Any provider, endpoint, capability, or invariant added to Go between those checkpoints becomes a blocking Rust parity requirement.

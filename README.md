# ya-router

`ya-router` is a local or self-hosted OpenAI-compatible router for:

- GitHub Copilot;
- ChatGPT-backed OpenAI Codex;
- OpenAI Platform API-key access;
- Kilo AI Gateway, including `kilo-auto/free`.

It exposes one client-facing API while keeping provider authentication, model routing, request translation, rate-limit failover, and transport policy inside provider implementations.

## Security model

The default deployment is **local-only**:

- the server binds to `127.0.0.1` by default;
- binding to a non-loopback address requires `YA_ROUTER_API_KEY`;
- CORS is disabled unless explicit origins are configured;
- ChatGPT OAuth credentials are never sent to `api.openai.com`;
- the official Codex credential store is read-only import data and is never rewritten;
- API keys and manual bearer tokens are read from environment variables or stdin, not command-line arguments;
- runtime config is written atomically with file mode `0600`.

ChatGPT-backed Codex uses a first-party-style OAuth/device flow and the ChatGPT Codex backend. That backend is distinct from the public OpenAI Platform API and may evolve independently. Use it in trusted local or controlled self-hosted environments.

## Endpoints

| Endpoint | Purpose |
|---|---|
| `GET /v1/models` | Aggregated, provider-prefixed model catalog |
| `POST /v1/chat/completions` | Chat Completions compatibility API |
| `POST /v1/responses` | Native Responses API path |
| `POST /v1/embeddings` | Embeddings through providers that support them |
| `GET /health` or `/health/live` | Process liveness |
| `GET /health/ready` | Readiness; returns `503` if no provider is authenticated |
| `GET /health/providers` | Redacted provider health and capabilities |

ChatGPT-authenticated Codex supports chat and native Responses. Embeddings require OpenAI Platform API-key mode; the router fails explicitly rather than sending a ChatGPT token to a Platform endpoint.

Kilo Gateway supports Chat Completions and native Responses passthrough. Its public model catalog is discovered dynamically. Without `KILO_API_KEY`, ya-router exposes only anonymous/free Kilo models.

## Build and validate

Requirements: Go 1.22 or newer. The production runtime is Go; no Rust runtime is currently present in the repository.

```bash
make build
make build-all
make check
```

`make check` runs:

```text
gofmt verification across cmd, internal, and src
go vet ./...
go test -race -count=1 ./...
build ya-router, ya-routerd, and ya
```

`make build` retains the compatibility output `./ya-router`. `make build-all`
also emits the service entrypoint `./ya-routerd` and the client foundation
`./ya`. The client transport and TUI are delivered by later roadmap issues;
the current `ya` binary intentionally exposes only help and version.

## Target managed-service architecture

The production data-plane behavior remains available through the Go
`ya-router` compatibility binary. Package and executable boundaries now also
provide `ya-routerd` and `ya` foundations so the daemon-owned control plane and
OS client can be delivered incrementally without changing the `/v1/*` contract:

- [Managed service and TUI architecture](docs/architecture/managed-service-and-tui.md)
- [Managed service and TUI delivery roadmap](docs/roadmaps/managed-service-and-tui-roadmap.md)
- [OpenSpec change](openspec/changes/add-managed-service-and-tui/proposal.md)

These documents remain the ordered implementation plan. The executable
foundations exist; the Control API and interactive TUI do not yet exist.

Data-plane handlers now use immutable, generation-tagged runtime snapshots.
Provider changes are constructed and validated before atomic publication;
in-flight requests retain their original snapshot while replaced instances
drain for a bounded, observable duration. Compiled-in provider descriptors
remain available independently from active instances, forming the lifecycle
foundation for the future Control API and TUI.

## Quick start

### GitHub Copilot

```bash
./ya-router auth copilot
./ya-router run
```

### ChatGPT-backed Codex

```bash
./ya-router auth codex
./ya-router run
```

The command performs the native ChatGPT/Codex device authorization flow. Credentials are stored in ya-router's local config. For backward compatibility, a single-account installation may import existing Codex credentials from `$CODEX_HOME/auth.json` or `~/.codex/auth.json`, but ya-router never modifies that file.

The OAuth client ID can be overridden for a managed deployment:

```bash
export YA_ROUTER_CODEX_OAUTH_CLIENT_ID=your_registered_client_id
```

### OpenAI Platform API key

Preferred server-side mode:

```bash
export OPENAI_API_KEY=sk-...
./ya-router run
```

To import a key into one account entry without exposing it in process arguments:

```bash
printf '%s\n' "$OPENAI_API_KEY" | ./ya-router auth codex --api-key-stdin
```

Manual ChatGPT access-token import is a recovery-only path:

```bash
printf '%s\n' "$CHATGPT_ACCESS_TOKEN" | ./ya-router auth codex --token-stdin
```

Manual access tokens do not carry a refresh token and should not be used for normal operation.

### Kilo Gateway and Auto Free

Enable anonymous access to Kilo's free model catalog:

```bash
./ya-router auth kilo
./ya-router run
```

The stable Auto Free model is exposed as `kilo/kilo-auto/free` and forwarded upstream as `kilo-auto/free`. Kilo documents anonymous free access as rate-limited by source IP.

For authenticated or paid models, prefer the process environment:

```bash
export KILO_API_KEY=your_kilo_api_key
./ya-router auth kilo
./ya-router run
```

Alternatively, import the key into ya-router's `0600` config through stdin:

```bash
printf '%s\n' "$KILO_API_KEY" | ./ya-router auth kilo --api-key-stdin
```

Auto Free may route requests to providers that log prompts and outputs or use them to improve services. Do not send confidential, personal, or regulated data through `kilo-auto/free`.

## Server exposure

### Loopback default

```bash
./ya-router run
# listens on 127.0.0.1:7071
```

### LAN or reverse-proxy deployment

```bash
export YA_ROUTER_LISTEN_ADDRESS=0.0.0.0
export YA_ROUTER_API_KEY="$(openssl rand -hex 32)"
./ya-router run
```

Clients authenticate with either:

```http
Authorization: Bearer <YA_ROUTER_API_KEY>
```

or:

```http
X-API-Key: <YA_ROUTER_API_KEY>
```

Configure browser origins explicitly:

```bash
export YA_ROUTER_CORS_ALLOWED_ORIGINS='https://app.example.com,https://admin.example.com'
```

No wildcard CORS response is emitted.

## API examples

### List models

```bash
curl http://127.0.0.1:7071/v1/models
```

### Chat Completions

```bash
curl http://127.0.0.1:7071/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "codex/gpt-5.4",
    "messages": [{"role": "user", "content": "Say hello"}]
  }'
```

### Native Responses

```bash
curl http://127.0.0.1:7071/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "codex/gpt-5.4",
    "input": "Return one short sentence.",
    "stream": false
  }'
```

### Structured output

Chat Completions `response_format` is translated to Responses `text.format` instead of being silently discarded:

```json
{
  "model": "codex/gpt-5.4",
  "messages": [{"role": "user", "content": "Return status"}],
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "status",
      "strict": true,
      "schema": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"}
        },
        "required": ["ok"],
        "additionalProperties": false
      }
    }
  }
}
```

Fields that cannot be represented safely by the selected transport return a client error. They are not silently dropped.

## Routing

Provider prefixes make routing deterministic:

| Prefix | Provider | Example |
|---|---|---|
| `github/` | GitHub Copilot | `github/gpt-4o` |
| `codex/` | OpenAI Codex | `codex/gpt-5.4` |
| `kilo/` | Kilo AI Gateway | `kilo/kilo-auto/free` |

Resolution order:

1. exact `routing.model_map` entry;
2. explicit provider prefix;
3. provider catalog discovery;
4. configured default provider only when the request omitted a model.

A prefixed request cannot enter another provider: `kilo/*`, `codex/*`, and `github/*` remain isolated. Unknown explicit bare model names fail. The default provider is used only when the request omitted a model. Ambiguous bare model names fail with a message asking for a prefix or explicit mapping.

Example config:

```json
{
  "routing": {
    "default_model": "gpt-5-mini",
    "default_provider": "copilot",
    "model_map": {
      "research-model": {
        "provider": "codex",
        "upstream_model": "gpt-5.4"
      }
    }
  }
}
```

## Multi-account pools

Authenticate named accounts independently:

```bash
./ya-router auth copilot --account work
./ya-router auth copilot --account personal
./ya-router auth codex --account work
./ya-router auth codex --account personal
```

Each Codex account uses its own access token, refresh token, account metadata, and cooldown state. The global official Codex store does not override account-pool entries.

For Codex runtime requests, an account-specific rate-limit response advances to the next eligible Codex account. Attempts are bounded by the Codex account count. When all Codex accounts are exhausted, the client receives the original HTTP rate-limit status and a typed `rate_limited` provider error. Copilot requests don't advance to another account or implicitly select a different Copilot model.

## Retry and failure semantics

- OAuth refresh retries only network failures, `429`, and `5xx`; permanent OAuth client errors fail immediately.
- An upstream `401` in ChatGPT mode triggers at most one refresh and one request retry.
- Unsafe POST requests are not retried after uncertain delivery unless the caller supplies an `Idempotency-Key`.
- Transport, authentication, entitlement, unsupported-capability, rate-limit, and invalid-request failures have distinct error kinds.
- Completion logs include the final HTTP status; a forwarded `401`, `403`, or `429` is not logged as a successful `200` operation.

## Configuration location

For compatibility with existing installations, the Go runtime currently reads:

```text
~/.local/share/github-copilot-svcs/config.json
```

The file remains the authoritative ya-router config until a versioned directory migration is introduced. Container startup preserves and secures that directory rather than silently moving credentials.

Inside Docker, the runtime also resolves `YA_ROUTER_CONFIG_PATH` (set by `entrypoint.sh`/compose env) to:

```text
/home/appuser/.local/share/github-copilot-svcs/config.json
```

If you run CLI commands against a container directly, this keeps `root` and `appuser` operations on the same credential file.

## Docker

```bash
docker build -t ya-router:local .
docker run --rm \
  -p 127.0.0.1:7071:7071 \
  -v "$HOME/.local/share/github-copilot-svcs:/home/appuser/.local/share/github-copilot-svcs" \
  ya-router:local
```

For non-loopback container exposure, set `YA_ROUTER_API_KEY` and place the service behind a TLS-terminating reverse proxy.

## Development workflow

Substantial changes use OpenSpec artifacts under `openspec/`. Before merging:

```bash
make check
docker build -t ya-router:check .
```

CI runs formatting verification, vet, race-enabled tests, and a production build. The manual release workflow cannot deploy unless validation succeeds.

## License

Apache License 2.0. See `LICENSE`.

# github-copilot-svcs

`github-copilot-svcs` is a local or self-hosted proxy that exposes OpenAI-compatible endpoints and routes requests to one or more upstream providers.

Current implementation supports:

- GitHub Copilot
- OpenAI Codex / OpenAI API

User-facing endpoints:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/embeddings`
- `GET /health`

If enabled in config, it also exposes profiling endpoints under `/debug/pprof/`.

Vietnamese operator guide: `docs/HUONG_DAN_SU_DUNG.md`

## What It Does

- Aggregates models from enabled providers.
- Routes chat and embeddings requests by requested model.
- Keeps provider authentication isolated.
- Migrates older single-provider Copilot configs to the current config schema.

## Quick Start

### 1. Build

```bash
make build

# or
go build -o github-copilot-svcs ./cmd/github-copilot-svcs
```

### 2. Prepare Runtime Config

```bash
mkdir -p ~/.local/share/github-copilot-svcs
cp config.example.json ~/.local/share/github-copilot-svcs/config.json
```

### 3. Authenticate Providers

GitHub Copilot:

```bash
./github-copilot-svcs auth copilot
```

Codex with ChatGPT login or existing Codex CLI login:

```bash
./github-copilot-svcs auth codex
```

This command now defaults to the same ChatGPT/device-auth flow used by the official `codex login` flow. It will:

- prefer an existing cached login under `CODEX_HOME` or `~/.codex`
- otherwise run `codex login --device-auth`
- then validate the resulting local credential cache

Codex with API key mode:

```bash
export OPENAI_API_KEY=your_api_key
./github-copilot-svcs auth codex --api-key
```

ChatGPT/device-auth mode requires:

- `providers.codex.enabled = true`
- `providers.codex.auth.mode = "chatgpt_device_auth"`
- a valid `auth.json` under `providers.codex.auth.codex_home` or `~/.codex`

### 4. Start The Service

```bash
./github-copilot-svcs run
```

### 5. Verify

```bash
curl http://localhost:7071/health
curl http://localhost:7071/v1/models
```

## CLI Commands

| Command | Purpose |
|---|---|
| `run` | Start the proxy server |
| `run --config-migrate merge|none|override` | Start and control config migration |
| `auth copilot` | Run GitHub device-flow authentication |
| `auth codex` | Use cached Codex login or run `codex login --device-auth`, then validate credentials |
| `status` | Show authentication state for configured providers |
| `config` | Print current config summary |
| `models` | List models from all enabled providers |
| `models --provider copilot` | List Copilot models only |
| `models --provider codex` | List Codex models only |
| `refresh` | Refresh or reload credentials |
| `refresh --provider copilot` | Force Copilot token refresh |
| `refresh --provider codex` | Re-read Codex credentials |
| `migrate-config --mode merge|override` | Run config migration manually |
| `version` | Print binary version |
| `help` | Show usage |

## User Workflows

### Copilot Only

1. Enable `providers.copilot.enabled`.
2. Run `./github-copilot-svcs auth copilot`.
3. Start the service with `./github-copilot-svcs run`.
4. Point your client to `http://localhost:7071/v1`.

### Codex Only

1. Set `providers.codex.enabled` to `true`.
2. Set `routing.default_provider` to `codex` if unmatched models should go there.
3. Configure `providers.codex.auth.mode`.
4. Run `./github-copilot-svcs auth codex`.
5. Start the service.

### Copilot And Codex Together

1. Enable both providers.
2. Authenticate each provider separately.
3. Use `routing.model_map` for known or ambiguous model names.
4. Inspect the merged model list with `./github-copilot-svcs models`.

## API Usage

### List Models

```bash
curl http://localhost:7071/v1/models
```

Behavior:

- Returns the merged model list from enabled providers.
- By default, only authenticated providers are shown.
- If `routing.show_unavailable_models` is `true`, unauthenticated providers may still appear if their implementation can provide models.

### Chat Completions

```bash
curl -X POST http://localhost:7071/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5-mini",
    "messages": [
      {"role": "user", "content": "Write a hello world in Go"}
    ]
  }'
```

Routing behavior:

- For Copilot chat, the service ignores the client-supplied `model`.
- It builds a dynamic free-model pool from official GitHub Docs plus the live Copilot `/models` catalog.
- It rotates globally across the eligible Copilot free models and patches the upstream request body itself.
- If one selected model fails before the response is committed, the same request shifts to the next eligible model automatically.

### Embeddings

```bash
curl -X POST http://localhost:7071/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-3-large",
    "input": "hello world"
  }'
```

### Health

```bash
curl http://localhost:7071/health
```

## Configuration

Runtime config path:

- Linux and macOS: `~/.local/share/github-copilot-svcs/config.json`
- In this repo's container image: `/home/appuser/.local/share/github-copilot-svcs/config.json`

Example:

```json
{
  "port": 7071,
  "config_version": 1,
  "enable_pprof": false,
  "routing": {
    "default_model": "gpt-5-mini",
    "default_provider": "copilot",
    "show_unavailable_models": false,
    "model_map": {
      "text-embedding-3-large": {
        "provider": "codex"
      }
    }
  },
  "providers": {
    "copilot": {
      "enabled": true,
      "auth": {
        "mode": "device_code"
      },
      "allowed_models": []
    },
    "codex": {
      "enabled": false,
      "auth": {
        "mode": "chatgpt_device_auth",
        "api_key_env": "OPENAI_API_KEY",
        "codex_home": "~/.codex"
      },
      "allowed_models": []
    }
  },
  "timeouts": {
    "http_client": 300,
    "server_read": 30,
    "server_write": 300,
    "server_idle": 120,
    "proxy_context": 300,
    "circuit_breaker": 30,
    "keep_alive": 30,
    "tls_handshake": 10,
    "dial_timeout": 10,
    "idle_conn_timeout": 90
  }
}
```

### Important Fields

#### `routing.default_model`

- Used by the legacy router when a request path still relies on normal model resolution.
- Copilot chat free-rotation does not use it as the request-time selector.

#### `routing.default_provider`

- Used when the requested model cannot be resolved via explicit mapping or model discovery.
- Copilot chat free-rotation bypasses this and goes directly to the Copilot provider.

#### `routing.model_map`

- Explicit routing rules.
- Recommended when the same model name may exist on multiple providers.
- Still relevant for embeddings and non-Copilot-chat paths.

#### `providers.<provider>.allowed_models`

- Applied inside each provider's model listing logic.
- Empty list means allow all discovered models for that provider.
- For Copilot chat rotation, the service uses the live upstream catalog and official GitHub Docs instead of this static list.

### Copilot Free Chat Rotation

- Chat requests to GitHub Copilot ignore the incoming `model` field.
- Eligible models are the intersection of:
  - models available on `Copilot Free`
  - models with `0` premium multiplier on paid plans
  - models currently returned by Copilot `/models`
- The docs trust source is cached on disk as a last-known-good snapshot.
- If the selected model errors before the response is committed, the request shifts immediately to the next eligible model.

#### `providers.copilot.auth`

- Uses `device_code` mode.
- Token state is persisted under this section after authentication.

#### `providers.codex.auth`

- `api_key` reads from the env var in `api_key_env`.
- `chatgpt_device_auth` reads local cached auth from `codex_home` or `~/.codex`.
- `auth codex` defaults to `chatgpt_device_auth`.

## Authentication Notes

### Copilot

- Uses GitHub OAuth device flow.
- Persists auth state in the runtime config file.
- Automatically refreshes tokens before expiry.
- `refresh --provider copilot` forces a refresh attempt.

### Codex

Supported credential modes:

- `api_key`
- `chatgpt_device_auth`

Operational guidance:

- `api_key` is the preferred server-side mode.
- `chatgpt_device_auth` is intended for trusted local or self-hosted environments.
- `~/.codex/auth.json` is secret material and must never be logged or exposed.

## Config Migration

Older flat configs are migrated automatically to config version `1`.

Modes:

- `merge`: preserve existing values and fill missing fields from defaults
- `override`: replace current config with defaults
- `none`: disable startup migration

Examples:

```bash
./github-copilot-svcs run --config-migrate merge
./github-copilot-svcs run --config-migrate none
./github-copilot-svcs migrate-config --mode override
```

## Docker

```bash
docker run -d \
  --name github-copilot-svcs \
  -p 7071:7071 \
  -v ./config:/home/appuser/.local/share/github-copilot-svcs \
  -e OPENAI_API_KEY=your_api_key \
  docker.x51.vn/dev/github-copilot-svcs:latest
```

First-time Copilot auth in container:

```bash
docker exec -it github-copilot-svcs /app/github-copilot-svcs auth copilot
```

## Troubleshooting

### `models` shows fewer models than expected

- Check `./github-copilot-svcs status`.
- Verify the provider is enabled.
- Verify `allowed_models` is not filtering your expected models.
- For Codex API-key mode, confirm the env var exists in the same process environment.

### Codex authentication fails

- For `api_key`, confirm `OPENAI_API_KEY` or the configured env var is set.
- For `chatgpt_device_auth`, confirm the auth cache exists and contains a usable access token.

### A request routes to the wrong provider

- Add an explicit entry in `routing.model_map`.
- Confirm the exact model string sent by the client.

### Need per-provider visibility

```bash
./github-copilot-svcs status
./github-copilot-svcs models --provider copilot
./github-copilot-svcs models --provider codex
```

## Development

```bash
make fmt
make vet
make test
```

## Local Release

Run the full local release cycle (Go build → Docker build → Docker push → git commit → git push):

```bash
# Required env vars
export DOCKER_REGISTRY_HOST=docker.x51.vn
export DOCKER_REGISTRY_USER=<your-username>
export DOCKER_REGISTRY_PASSWORD=<your-password>

make release
```

Override the version tag (default: `YYYYMMDD-local`):

```bash
VERSION=v2.1.0 make release
```

Dry-run (prints every command, skips all destructive operations):

```bash
DRY_RUN=1 make release
```

Composable sub-targets:

```bash
make docker-build          # build Docker image only
make docker-push           # push image to registry (requires env vars)
make git-commit-push       # commit all changes and push to git
```

Alternatively, use the wrapper script directly:

```bash
./scripts/release.sh
DRY_RUN=1 ./scripts/release.sh
VERSION=v2.1.0 ./scripts/release.sh
```

## License

Apache License 2.0. See `LICENSE`.

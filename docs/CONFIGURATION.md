# ya-router configuration guide

## Configuration source

The production Go runtime reads:

```text
~/.local/share/github-copilot-svcs/config.json
```

The historical directory name is retained to avoid silently losing existing routing settings and credentials. The file is written atomically with mode `0600`.

Start from `config.example.json` and run:

```bash
./ya-router config
./ya-router status
```

## Server security environment

Server exposure is intentionally controlled outside the JSON credential file.

| Environment variable | Default | Purpose |
|---|---|---|
| `YA_ROUTER_LISTEN_ADDRESS` | `127.0.0.1` | Address to bind |
| `YA_ROUTER_API_KEY` | empty | Inbound proxy credential; mandatory for non-loopback binding |
| `YA_ROUTER_CORS_ALLOWED_ORIGINS` | empty | Comma-separated browser-origin allowlist |
| `YA_ROUTER_CODEX_OAUTH_CLIENT_ID` | Codex-compatible default | OAuth client override for managed deployments |
| `OPENAI_API_KEY` | empty | OpenAI Platform API key |
| `CODEX_HOME` | `~/.codex` | Read-only source for a legacy/single-account Codex credential import |
| `KILO_API_KEY` | empty | Kilo Gateway API key; optional for free anonymous models |
| `KILO_ORG_ID` | empty | Kilo organization context |
| `KILO_GATEWAY_BASE_URL` | `https://api.kilo.ai/api/gateway` | Full Kilo Gateway base URL override |

Examples:

```bash
# Local-only; no inbound proxy key required.
./ya-router run

# Network deployment; a proxy key is mandatory.
export YA_ROUTER_LISTEN_ADDRESS=0.0.0.0
export YA_ROUTER_API_KEY="$(openssl rand -hex 32)"
export YA_ROUTER_CORS_ALLOWED_ORIGINS='https://app.example.com'
./ya-router run
```

The service refuses to start on a non-loopback address when `YA_ROUTER_API_KEY` is missing.

## Top-level schema

```json
{
  "port": 7071,
  "config_version": 1,
  "enable_pprof": false,
  "routing": {},
  "providers": {},
  "timeouts": {}
}
```

### `port`

Listening port. Default: `7071`.

### `enable_pprof`

Enables `/debug/pprof/`. The endpoints are still covered by the inbound access policy. Keep this disabled outside controlled diagnostics.

## Routing

```json
{
  "routing": {
    "default_model": "gpt-5-mini",
    "default_provider": "copilot",
    "show_unavailable_models": false,
    "model_map": {
      "research-model": {
        "provider": "codex",
        "upstream_model": "gpt-5.4"
      }
    }
  }
}
```

### Resolution order

1. Exact `model_map` key.
2. Bare `model_map` key after removing a recognized provider prefix.
3. Explicit provider prefix.
4. Provider catalog discovery.
5. Configured default provider only when the request omitted the model.

Explicit prefixes are authoritative:

| Prefix | Provider |
|---|---|
| `github/` | GitHub Copilot |
| `codex/` | OpenAI Codex |
| `kilo/` | Kilo AI Gateway |

The prefix is removed before forwarding upstream. A model present in multiple providers requires a prefix or `model_map` rule. Unknown explicit bare model names fail. The default provider is used only when the request omitted the model.

### `show_unavailable_models`

When false, `/v1/models` hides providers that are not authenticated. Set true only for diagnostics.

## GitHub Copilot provider

```json
{
  "providers": {
    "copilot": {
      "enabled": true,
      "auth": {"mode": "device_code"},
      "accounts": [],
      "account_cooldown_seconds": 300,
      "allowed_models": []
    }
  }
}
```

Authenticate:

```bash
./ya-router auth copilot
./ya-router auth copilot --account work
```

Copilot chat only receives requests selected by `model_map`, a `github/` prefix, or provider catalog discovery. It cannot intercept `codex/*` requests, explicit Codex mappings, or unknown bare model names.

## Codex provider

Codex has two distinct authentication and transport modes.

### ChatGPT mode

```json
{
  "providers": {
    "codex": {
      "enabled": true,
      "auth": {"mode": "chatgpt"},
      "accounts": [],
      "account_cooldown_seconds": 300,
      "allowed_models": [],
      "chatgpt_base_url": "https://chatgpt.com/backend-api/codex/"
    }
  }
}
```

Authenticate with device OAuth:

```bash
./ya-router auth codex
./ya-router auth codex --account work
```

Properties:

- backend: ChatGPT Codex Responses;
- credentials: ya-router config account entry;
- supported capabilities: chat and native Responses;
- unsupported capability: embeddings;
- `401` handling: one refresh and one request retry;
- official `$CODEX_HOME/auth.json`: read-only fallback import for legacy/single-account use only.

The global official Codex store never overrides a populated account-pool entry.

### API-key mode

Use the process environment:

```bash
export OPENAI_API_KEY=sk-...
```

or import from stdin:

```bash
printf '%s\n' "$OPENAI_API_KEY" | ./ya-router auth codex --api-key-stdin
```

Properties:

- backend: `https://api.openai.com/v1`;
- supported capabilities: chat, native Responses, and embeddings;
- no OAuth refresh;
- ChatGPT account metadata is not sent.

The runtime uses `OPENAI_API_KEY`; `api_key_env` is not part of config version 1.

### Manual bearer-token fallback

```bash
printf '%s\n' "$CHATGPT_ACCESS_TOKEN" | ./ya-router auth codex --token-stdin
```

This path is intended for recovery only. Without a refresh token, reauthentication is required when the access token expires.

## Kilo Gateway provider

Kilo is disabled by default. Enable anonymous access to free models with:

```bash
./ya-router auth kilo
```

Or configure it directly:

```json
{
  "providers": {
    "kilo": {
      "enabled": true,
      "allow_anonymous": true,
      "organization_id": "",
      "base_url": "https://api.kilo.ai/api/gateway",
      "allowed_models": []
    }
  }
}
```

The provider discovers `GET /models`, forwards Chat Completions to `/chat/completions`, and passes native Responses requests to `/responses`. It intentionally does not advertise embeddings because embeddings are not part of Kilo's documented public Gateway contract.

### Auto Free

Use the client-facing model ID:

```text
kilo/kilo-auto/free
```

ya-router removes only its leading `kilo/` namespace and sends `kilo-auto/free` upstream. Without an API key, the catalog and proxy are restricted to IDs marked free by Kilo, IDs ending in `:free`, `openrouter/free`, and `kilo-auto/free`. Paid IDs fail before an upstream request is made.

Anonymous access can be disabled with `"allow_anonymous": false`. For authenticated models, prefer `KILO_API_KEY`; importing through stdin is also supported:

```bash
printf '%s\n' "$KILO_API_KEY" | ./ya-router auth kilo --api-key-stdin
```

The inbound ya-router API credential is never forwarded. Kilo requests receive only the server-owned Kilo credential, optional organization ID, and a small allowlist of Kilo task/mode headers.

Auto Free may route requests to providers that log prompts and outputs or use them to improve services. Do not submit confidential, personal, or regulated data through Auto Free.

## Multi-account pools

```json
{
  "providers": {
    "codex": {
      "enabled": true,
      "accounts": [
        {
          "label": "personal",
          "auth": {
            "mode": "chatgpt"
          }
        },
        {
          "label": "platform",
          "auth": {
            "mode": "api_key"
          }
        }
      ],
      "account_cooldown_seconds": 300
    }
  }
}
```

Each account owns its own auth state. On an account rate-limit response, the request can advance to the next eligible account. The attempt count is bounded by the number of accounts.

`LastLimitedAt` is persisted for compatibility. The current scheduler uses the configured cooldown duration; richer upstream-reset scheduling remains a future config-version change.

## Structured outputs

For `/v1/chat/completions`, the router maps:

```text
response_format.json_schema
    -> text.format.json_schema
```

It also preserves `tool_choice`, `parallel_tool_calls`, tools, reasoning, and supported metadata. Parameters that cannot be represented by the selected Responses transport return a request error rather than being silently removed.

For callers already using Responses semantics, prefer:

```text
POST /v1/responses
```

The native endpoint preserves Responses output and SSE events without converting them into Chat Completions chunks.

## Retry behavior

- Network, `408`, and `5xx` retry is bounded.
- Unsafe POST requests require an `Idempotency-Key` before retry after uncertain delivery.
- `429` is handled by account failover rather than blind transport retry.
- OAuth refresh retries only transient network failures, `429`, and `5xx`.
- Permanent OAuth `4xx` failures require reauthentication.

## Timeouts

```json
{
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

All values are seconds. Long-lived streaming requests must fit within both `http_client` and `proxy_context`.

## Health and operations

```bash
curl http://127.0.0.1:7071/health/live
curl http://127.0.0.1:7071/health/ready
curl http://127.0.0.1:7071/health/providers
```

`/health/providers` returns only provider ID, name, authentication state, refreshability, and capabilities. It does not return tokens or account IDs.

## Migration notes

- Legacy top-level provider auth is promoted to account label `primary` when required.
- Existing config path and config version remain supported.
- ChatGPT tokens previously copied into the official Codex store are no longer written there by ya-router.
- Existing secret-bearing `--api-key` and `--token` command-line flags have been replaced by stdin flags to prevent process-list and shell-history leakage.

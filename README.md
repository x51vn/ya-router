# ya-router

`ya-router` is a local or self-hosted OpenAI-compatible gateway that routes requests across:

- GitHub Copilot;
- OpenAI Codex through ChatGPT device authentication;
- OpenAI Platform API-key mode;
- Kilo AI Gateway, including anonymous `kilo-auto/free`.

Clients can call one public model, `thiendu`, while the router selects one currently eligible model/provider target before dispatch.

## Current implementation

The current Go runtime includes:

| Area | Implemented |
|---|---|
| OpenAI-compatible data plane | `/v1/models`, `/v1/chat/completions`, `/v1/responses`, and `/v1/embeddings` |
| Providers | GitHub Copilot, Codex, and Kilo |
| Automatic routing | Generic `routing.virtual_models`, default `thiendu`, capability/health/auth/catalog/allowlist/cooldown-aware priority selection |
| Explicit routing | `github/*`, `codex/*`, `kilo/*`, and `routing.model_map` |
| Managed service | `ya-routerd`, immutable runtime snapshots, provider hot reload/drain, revisioned configuration |
| Control plane | Private Unix-socket Control API by default; optional HTTPS client transport |
| Operator client | `ya` Bubble Tea dashboard plus scriptable text/JSON commands |
| Provider setup | Device-code, API-key, anonymous Kilo, redacted secret posture, durable auth operations |
| Linux delivery | Linux/amd64 artifacts, checksums, systemd unit, durable state, backup/rollback runbook |
| Docker | Compatibility data-plane image using the historical `ya-router` binary |

The automated test suite uses mock upstreams and does not require live provider credentials. Real GitHub Copilot, Codex, and Kilo smoke tests remain an operator/release-environment responsibility.

Application logs are written to stderr and to `logs/ya-router.log` by default.
The local file rotates at 5 MiB and retains two files in total; see
[Configuration](docs/CONFIGURATION.md#application-log-files) for the file path
and rotation settings.

See [MVP1 implementation status](docs/MVP1_STATUS.md) for the detailed delivery state and explicit deferrals.

## Architecture

```text
OpenAI-compatible client
        |
        | HTTP /v1/*
        v
+--------------------------+
|       ya-routerd         |
|                          |
| OpenAI compatibility     |
| automatic router         |
| runtime/provider manager |
+------------+-------------+
             |
       one selected target
             |
    +--------+--------+---------+
    |                 |         |
GitHub Copilot      Codex      Kilo

ya TUI / CLI
    |
    | private Control API
    | Unix socket by default
    v
ya-routerd
```

The repository builds three binaries:

| Binary | Purpose |
|---|---|
| `GET /v1/models` | Aggregated, provider-prefixed model catalog |
| `POST /v1/chat/completions` | Chat Completions compatibility API |
| `POST /v1/responses` | Native Responses API path |
| `POST /v1/messages` | Anthropic Messages facade for Claude Code |
| `POST /v1/embeddings` | Embeddings through providers that support them |
| `GET /health` or `/health/live` | Process liveness |
| `GET /health/ready` | Readiness; returns `503` if no provider is authenticated |
| `GET /health/providers` | Redacted provider health and capabilities |

Resolution order is:

1. exact `routing.model_map`;
2. explicit provider prefix;
3. configured virtual model such as `thiendu`;
4. unprefixed provider-catalog discovery;
5. configured default provider only when the request omitted a model.

Selection happens before dispatch and remains pinned for the complete request. A failed selected request is returned to the client and is **not replayed** to another provider. A later request may choose a different eligible target while the previous target is unhealthy or in cooldown.

## Security defaults

- The data plane binds to `127.0.0.1` by default.
- A non-loopback bind requires `YA_ROUTER_API_KEY`.
- Health endpoints remain available without the data-plane credential.
- CORS is disabled unless `YA_ROUTER_CORS_ALLOWED_ORIGINS` is configured.
- The managed Control API uses a private Unix socket by default.
- Runtime state and secrets use owner-only directories and atomic `0600` files.
- The official Codex credential store is read-only import data.
- Secret values are accepted through environment variables, stdin, or masked TUI input; they are never returned by the Control API.
- Prompt, completion, credential, and raw upstream error bodies are excluded from routing diagnostics.

## Claude Code gateway

Claude Code can use the Anthropic Messages facade through `ANTHROPIC_BASE_URL`.
Configure an explicit `claude-*` alias for a Responses-capable canonical model,
then point Claude Code at the local gateway. The full request/streaming contract,
supported content blocks, capability limits, and troubleshooting guidance are in
[Anthropic and Claude Code compatibility](docs/ANTHROPIC_COMPATIBILITY.md).

## Build and validate

### Requirements

- Ubuntu 22.04 or newer;
- Go 1.22 or newer;
- Git, Make, a C toolchain for race-enabled tests, CA certificates, `curl`, and `openssl`.

Install the OS packages:

```bash
sudo apt update
sudo apt install -y git make build-essential ca-certificates curl openssl golang-go
go version
```

The reported Go version must be `go1.22` or newer. Install a newer Go toolchain before continuing if the Ubuntu package is older.

### Clone and validate

```bash
git clone https://github.com/duvu/ya-router.git
cd ya-router

make check
```

`make check` runs:

```text
gofmt verification
go vet ./...
go test -race -count=1 ./...
build ya-router, ya-routerd, and ya
```

Build without running the full validation suite:

```bash
make build-all

./ya-router version
./ya-routerd version
./ya version
```

### Build verified Linux/amd64 artifacts

```bash
VERSION="$(git describe --always --dirty)" make release-linux

cd dist/linux-amd64
sha256sum -c checksums.txt
cat build-info.txt
cd ../..
```

Artifacts are written to:

```text
dist/linux-amd64/
├── ya-router
├── ya-routerd
├── ya
├── build-info.txt
└── checksums.txt
```

The packaging script currently targets Linux/amd64. On another architecture, `make build-all` builds native binaries but does not create this release directory.

## Quick local run

Start the managed daemon as the current user:

```bash
./ya-routerd run
```

In another terminal:

```bash
./ya
```

With both processes running under the same user, `ya` finds the default Control socket next to:

```text
~/.local/share/github-copilot-svcs/config.json
```

Open the action palette with `a`, select a provider with arrow keys or `j`/`k`, then use:

| Key | Action |
|---|---|
| `e` | Enable or disable the selected provider |
| `c` | Start device-code authentication, or anonymous Kilo mode |
| `p` | Enter a masked Codex or Kilo API key |
| `m` | Refresh provider model catalogs |
| `x` | Cancel a cancelable authentication operation |
| `r` | Refresh/reconnect |
| `q` | Quit the client without stopping the daemon |

The compatibility workflow remains available:

```bash
./ya-router auth copilot
./ya-router auth codex
./ya-router auth kilo
./ya-router run
```

## Deploy on Ubuntu with systemd

This is the recommended single-host managed deployment. It installs:

```text
/usr/local/bin/ya-routerd
/usr/local/bin/ya
/usr/local/bin/ya-router
/etc/ya-router/ya-router.env
/var/lib/ya-router/config.json
/var/lib/ya-router/secrets.json
/var/lib/ya-router/operations.json
/run/ya-router/control.sock
```

### 1. Create the service account and directories

Run from the repository root after `make release-linux`:

```bash
getent passwd ya-router >/dev/null || \
  sudo useradd --system \
    --home-dir /var/lib/ya-router \
    --shell /usr/sbin/nologin \
    ya-router

sudo install -d -o ya-router -g ya-router -m 0700 /var/lib/ya-router
sudo install -d -o root -g ya-router -m 0750 /etc/ya-router
```

### 2. Install binaries and systemd files

```bash
sudo install -m 0755 \
  dist/linux-amd64/ya-routerd \
  dist/linux-amd64/ya \
  /usr/local/bin/

sudo install -m 0755 \
  dist/linux-amd64/ya-router \
  /usr/local/bin/ya-router

sudo install -m 0644 \
  packaging/systemd/ya-router.service \
  /etc/systemd/system/ya-router.service

sudo install -m 0640 -o root -g ya-router \
  packaging/systemd/ya-router.env.example \
  /etc/ya-router/ya-router.env
```

### 3. Configure environment credentials

Edit the owner-restricted environment file:

```bash
sudoedit /etc/ya-router/ya-router.env
```

Available settings:

```dotenv
# Recommended even for loopback deployments.
YA_ROUTER_API_KEY=

# Optional OpenAI Platform API-key mode.
OPENAI_API_KEY=

# Optional authenticated Kilo access.
KILO_API_KEY=
```

Generate a strong data-plane key with:

```bash
openssl rand -hex 32
```

ChatGPT/Codex and GitHub Copilot device authentication can be completed later from the TUI without placing those credentials in the environment file.

### 4. Start the service

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ya-router

sudo systemctl status --no-pager ya-router
sudo journalctl -u ya-router -n 100 --no-pager
```

The service listens on:

```text
Data plane:  http://127.0.0.1:7071
Control API: /run/ya-router/control.sock
```

### 5. Open the TUI

The systemd Control socket is private and owned by the `ya-router` service account. Run the client as that account and explicitly pass the daemon socket:

```bash
sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya
```

Scriptable examples:

```bash
sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya providers

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya routing --json

sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya operations
```

Do not rely on `sudo -u ya-router /usr/local/bin/ya` without the socket variable: the interactive shell does not inherit the service unit's `YA_ROUTER_CONTROL_SOCKET`.

## Test the OpenAI-compatible API

When `YA_ROUTER_API_KEY` is configured, export the same value in the operator shell:

```bash
export YA_ROUTER_API_KEY='the-value-from-/etc/ya-router/ya-router.env'
```

List models:

```bash
curl --fail http://127.0.0.1:7071/v1/models \
  -H "Authorization: Bearer $YA_ROUTER_API_KEY"
```

Chat Completions through automatic routing:

```bash
curl --fail http://127.0.0.1:7071/v1/chat/completions \
  -H "Authorization: Bearer $YA_ROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "model": "thiendu",
    "messages": [
      {"role": "user", "content": "Reply with one short sentence."}
    ]
  }'
```

Streaming Responses:

```bash
curl --no-buffer http://127.0.0.1:7071/v1/responses \
  -H "Authorization: Bearer $YA_ROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "model": "thiendu",
    "input": "Reply with one short sentence.",
    "stream": true
  }'
```

When no data-plane key is configured for a loopback-only deployment, omit the authorization header.

## Health checks and troubleshooting

```bash
curl --fail http://127.0.0.1:7071/health
curl --fail http://127.0.0.1:7071/health/ready
curl --fail http://127.0.0.1:7071/health/providers
curl --fail http://127.0.0.1:7071/health/umbrella

sudo -u ya-router -H \
  curl --unix-socket /run/ya-router/control.sock \
  http://unix/control/v1/meta

sudo journalctl -u ya-router -f
```

Useful client commands:

```bash
YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock ya meta
YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock ya providers
YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock ya models --refresh
YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock ya routing
YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock ya operations
```

Run them as `root` or the `ya-router` service account because the default socket is owner-only.

## Upgrade, rollback, and backup

Before replacing binaries:

```bash
sudo systemctl stop ya-router

sudo install -d -m 0700 /var/backups/ya-router
sudo tar -C /var/lib \
  -czf "/var/backups/ya-router/ya-router-$(date +%F-%H%M%S).tar.gz" \
  ya-router

sudo install -m 0755 \
  dist/linux-amd64/ya-routerd \
  dist/linux-amd64/ya \
  /usr/local/bin/

sudo install -m 0755 \
  dist/linux-amd64/ya-router \
  /usr/local/bin/ya-router

sudo systemctl start ya-router
```

Verify recovery:

```bash
sudo -u ya-router -H \
  env YA_ROUTER_CONTROL_SOCKET=/run/ya-router/control.sock \
  /usr/local/bin/ya providers

curl --fail http://127.0.0.1:7071/health/ready
```

Rollback replaces the binaries with a previously verified artifact and leaves `/var/lib/ya-router` intact. Stop the daemon before copying or restoring state files.

The detailed single-host runbook is in [docs/LINUX_DEPLOYMENT.md](docs/LINUX_DEPLOYMENT.md).

## Docker compatibility deployment

The current `Dockerfile` builds the historical `ya-router` compatibility binary. It provides the OpenAI-compatible data plane, but it does **not** package the separate `ya-routerd` plus `ya` TUI workflow.

```bash
docker build -t ya-router:local .

docker run --rm \
  -p 127.0.0.1:7071:7071 \
  -v "$HOME/.local/share/github-copilot-svcs:/home/appuser/.local/share/github-copilot-svcs" \
  ya-router:local
```

For the full managed service and TUI on Ubuntu, use the systemd deployment above.

For non-loopback container exposure, configure `YA_ROUTER_API_KEY` and use a TLS-terminating reverse proxy.

## Configuration

Local compatibility configuration:

```text
~/.local/share/github-copilot-svcs/config.json
```

Managed systemd state:

```text
/var/lib/ya-router/config.json
/var/lib/ya-router/secrets.json
/var/lib/ya-router/operations.json
```

Environment overrides:

```text
YA_ROUTER_CONFIG_DIR
YA_ROUTER_CONFIG_PATH
YA_ROUTER_SECRETS_PATH
YA_ROUTER_OPERATIONS_PATH
YA_ROUTER_CONTROL_SOCKET
YA_ROUTER_LISTEN_ADDRESS
YA_ROUTER_API_KEY
YA_ROUTER_CORS_ALLOWED_ORIGINS
OPENAI_API_KEY
KILO_API_KEY
```

See:

- [Configuration reference](docs/CONFIGURATION.md)
- [Control API](docs/CONTROL_API.md)
- [OpenAI compatibility](docs/OPENAI_COMPATIBILITY.md)
- [Provider onboarding](docs/PROVIDER_ONBOARDING.md)
- [Managed service and TUI architecture](docs/architecture/managed-service-and-tui.md)
- [Automatic routing architecture](docs/architecture/umbrella-model-routing.md)

## Current limitations

- The MVP TUI does not edit generic virtual-model policies or perform account CRUD.
- The TUI does not start, stop, or restart systemd/Docker.
- OIDC and persisted named client profiles are not part of MVP1.
- Linux release packaging currently targets amd64.
- The Docker image packages the compatibility binary, not the managed TUI pair.
- Real-provider smoke tests require operator-owned credentials and entitlements.
- Advanced cost-, latency-, quality-, weighted-, or prompt-aware routing is not implemented.
- Same-request cross-provider replay is intentionally not supported.

## Development

Before submitting changes:

```bash
make check
docker build -t ya-router:check .
```

CI validates formatting, `go vet`, race-enabled tests, and all three Go binaries. The manual release workflow can also publish Linux artifacts and a Docker image after validation.

Substantial changes use OpenSpec artifacts under `openspec/`.

## License

Apache License 2.0. See [LICENSE](LICENSE).

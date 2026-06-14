# AGENTS.md

Compact orientation for OpenCode sessions in this repo. Read `README.md` for user-facing detail; this file only captures things an agent would otherwise miss.

## What this is

Single-binary Go service (`github-copilot-svcs`) that exposes OpenAI-compatible endpoints (`/v1/models`, `/v1/chat/completions`, `/v1/embeddings`, `/health`) and proxies to GitHub Copilot and/or OpenAI Codex. CLI dispatcher in `cmd/github-copilot-svcs/main.go` (subcommands: `run`, `auth`, `status`, `config`, `models`, `refresh`, `migrate-config`, `version`).

## Layout (non-obvious bits)

- **New modular layout** (production code lives in `internal/` and `cmd/`):
  - `cmd/github-copilot-svcs/` — CLI entrypoint (`main.go`, `cli.go`), `package main`
  - `internal/config/` — Config struct, LoadConfig, SaveConfig, MigrateConfig
  - `internal/auth/` — CopilotAuthenticate, CodexAuthenticate, token refresh
  - `internal/httputil/` — HTTP infrastructure: CircuitBreaker, CoalescingCache, WorkerPool, MakeRequestWithRetry, StreamResponse
  - `internal/provider/` — Provider interface, ProviderRegistry, CopilotProvider, CodexProvider, model cache, prefixes, transform, responses adapter
  - `internal/proxy/` — ProxyHandler, ModelRouter, server setup, health endpoint
  - `internal/types/` — Shared domain types (Model, ModelList)
- **Legacy**: `src/` still exists as the old flat `package main` layout with tests. It is kept intact; tests there continue to pass. Do NOT add new production code to `src/`.
- Build target is `./cmd/github-copilot-svcs`, **not** `./src` or `./...`.
- `go.mod` declares `go 1.21`, but Dockerfile and CI use `go 1.22`. There is **no `go.sum`** (zero external deps); do not introduce one casually.
- `config/config.json` in the repo root is the **bind-mount target for docker-compose**, not the runtime config for local `go run`. The local runtime config lives at `~/.local/share/github-copilot-svcs/config.json` (see `config.example.json`).
- `openspec/` + `.opencode/skills/openspec-*` drive a spec-driven change workflow. For non-trivial features prefer the `openspec-propose` / `openspec-apply-change` / `openspec-archive-change` skills over ad-hoc edits. Slash commands: `/opsx-propose`, `/opsx-apply`, `/opsx-archive`, `/opsx-explore`.
- `docs/` holds dated PRDs and request cards that document past decisions; consult them before re-deciding routing/auth/transport behaviour.

## Build, test, verify (exact commands)

```bash
make build                                       # go build -ldflags=... -o github-copilot-svcs ./cmd/github-copilot-svcs
make fmt                                         # go fmt ./internal/... ./cmd/... ./src/...
make vet                                         # go vet ./internal/... ./cmd/... ./src/...
make test                                        # go test ./internal/... ./cmd/... ./src/...
go test ./src/... -run TestName                  # single test in legacy src/
go test ./internal/provider/... -run TestName    # single test in internal package
```

Order before declaring done: `make fmt && make vet && make test && make build`.

CI (`.github/workflows/ci-cd.yml`) runs on **self-hosted runners** and uses `go test ./src/... || true` — tests do **not** block deploy. Do not rely on CI to catch regressions; run tests locally.

## Runtime / auth gotchas

- `auth codex` defaults to `chatgpt_device_auth` and shells out to the external `codex` CLI (`codex login --device-auth`). It reads `~/.codex/auth.json` — treat as secret, never log or echo.
- Copilot chat **ignores the client `model` field** and rotates across an eligible free-tier pool derived from GitHub Docs ∪ live Copilot `/models`, with on-error failover. `routing.default_model` and `providers.copilot.allowed_models` do **not** drive this path. Embeddings and non-Copilot-chat paths still use normal routing (`routing.model_map`, `default_provider`).
- Config migration runs on every `run` (default `--config-migrate merge`). Preserve `ConfigMigrationMode` semantics (`none|merge|override`) when touching `internal/config/config.go`.
- pprof is gated on `enable_pprof` in config; off by default.

## Deploy pipeline (be careful)

`main` push triggers: build → push image to `docker.x51.vn/dev/github-copilot-svcs:{date-runnumber, latest}` → patch `x51vn/deployment` repo's `server2/docker-compose.yml` via `sed` → ssh deploy. A merge to `main` ships to production. Never commit/push without explicit user request.

## Style conventions

- New code goes in `internal/<package>/` with responsibility-based package names (not `package main`).
- Errors propagate up to `cmd/github-copilot-svcs/main.go` which prints `"<verb> failed: %v"` and `os.Exit(1)`. Follow that pattern for new subcommands.
- Unit tests sit alongside sources (`*_test.go` beside the `.go` file in the same package). Integration-style tests remain in `src/` using the old mock helpers. Use table-driven tests as in `src/transform_test.go`, `src/proxy_test.go`.
- Vietnamese operator-facing doc (`docs/HUONG_DAN_SU_DUNG.md`) is intentional; keep it in sync with English README when changing user-visible CLI/config.

## Don't

- Don't add deps without confirming — repo is currently stdlib-only.
- Don't use `go build ./...` or `go test ./...`; always scope to `./internal/... ./cmd/...` (or `./src/...` for legacy tests).
- Don't edit `config/config.json` to change defaults — it's volume content. Defaults live in `internal/config/config.go` and `config.example.json`.
- Don't add new production code to `src/` — it is legacy and kept only for its test suite.
- Don't bypass the openspec workflow for substantial changes when the user is using it.

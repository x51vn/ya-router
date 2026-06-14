## Context

The repo currently has a `Makefile` with targets for `build`, `fmt`, `vet`, `test`, but no targets for Docker operations or git operations. The CI/CD pipeline (`ci-cd.yml`) handles Docker build/push and deployment automatically on `workflow_dispatch`, but developers have no local equivalent for ad-hoc releases or testing the full pipeline manually.

Docker registry is `docker.x51.vn`, credentials are env vars (`DOCKER_REGISTRY_HOST`, `DOCKER_REGISTRY_USER`, `DOCKER_REGISTRY_PASSWORD`). Version tagging uses `YYYYMMDD-<run_number>` format in CI.

## Goals / Non-Goals

**Goals:**
- Single `make release` command that runs the complete release cycle locally
- Composable sub-targets (`docker-build`, `docker-push`) usable independently
- Validate required env vars before executing destructive steps
- Mirror the CI/CD version tagging convention (`YYYYMMDD-<n>` or git short SHA)
- Commit all local changes and push to git as part of the release flow

**Non-Goals:**
- Replacing or modifying the GitHub Actions CI/CD pipeline
- Deployment to server (remains CI/CD responsibility)
- Interactive prompts or TUI — pure shell/make
- Multi-arch Docker builds

## Decisions

### 1. Makefile targets vs. standalone script

**Decision**: Makefile targets with a thin `scripts/release.sh` wrapper for env validation.

**Rationale**: Makefile keeps the interface consistent with existing workflow (`make build`, `make test`). The shell script handles env var checks and sequencing logic that is awkward in make syntax. Alternatives considered:
- Pure Makefile with `.PHONY` guard targets — hard to do robust env validation and error messaging
- Python/Go script — overkill for shell-level orchestration, adds a runtime dep

### 2. Version tagging strategy

**Decision**: Default to `$(date +%Y%m%d)-local` for local releases; allow override via `VERSION` env var.

**Rationale**: Mirrors CI's `YYYYMMDD-<run_number>` pattern. The `-local` suffix distinguishes local builds from CI builds to avoid registry collisions. `VERSION=v1.2.3 make release` allows explicit override.

### 3. Git commit scope

**Decision**: `git add -A` (all tracked + untracked changes) then `git commit -m "chore: release <VERSION>"` then `git push`.

**Rationale**: Release workflow implies the working tree is intentionally clean at release time. Using `-A` is explicit. Commit message follows the repo's existing `ci:` / `chore:` convention.

### 4. Error handling

**Decision**: `set -euo pipefail` in the shell script; each step prints its action before executing.

**Rationale**: Fail fast on any step. Verbose output makes it clear which step failed. No silent swallowing of errors.

## Risks / Trade-offs

- **Accidental commit of unintended files** → Mitigation: script prints `git status` before committing and prompts user to confirm (or `DRY_RUN=1` skips destructive steps)
- **Registry credentials in env** → Mitigation: script validates vars are non-empty but never echoes values; same pattern already used in CI
- **`latest` tag overwrite** → Mitigation: documented behavior; consistent with CI pipeline's existing `latest` push

## Migration Plan

1. Add `docker-build`, `docker-push`, `release` targets to `Makefile`
2. Create `scripts/release.sh` with env validation and step sequencing
3. Update `README.md` with usage instructions for the new targets
4. No rollback needed — additive changes only

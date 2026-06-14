## Why

The project lacks a single-command local workflow to build the Go binary, build and push the Docker image to the registry, then commit and push the codebase to git. Developers currently must run these steps manually in the correct order, which is error-prone and slow during iterative releases.

## What Changes

- Add a `make release` (or equivalent) Makefile target that chains: `make build` → Docker build → Docker tag+push → `git add -A && git commit && git push`
- Add a helper shell script (`scripts/release.sh`) that wraps the above with environment variable validation, version tagging, and error handling
- Add supporting `make` targets: `make docker-build`, `make docker-push` for composability

## Capabilities

### New Capabilities
- `local-release-workflow`: A single entry-point command (`make release`) that runs the full local release cycle: Go build → Docker build → Docker push → git commit → git push, with configurable version tag and dry-run support

### Modified Capabilities
<!-- No existing spec-level behavior changes -->

## Impact

- **`Makefile`**: New targets added (`docker-build`, `docker-push`, `release`)
- **`scripts/release.sh`**: New script (wraps Makefile targets, validates env vars `DOCKER_REGISTRY_HOST`, `DOCKER_REGISTRY_USER`, `DOCKER_REGISTRY_PASSWORD`)
- **No source code changes**: No changes to `src/` Go files
- **No CI/CD changes**: The existing GitHub Actions workflow remains untouched; this is a local developer workflow

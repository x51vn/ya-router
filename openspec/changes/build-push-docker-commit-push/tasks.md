## 1. Makefile targets

- [x] 1.1 Add `docker-build` target: runs `docker build --build-arg IMAGE_VERSION=$(VERSION)` tagging as `$(DOCKER_REGISTRY_HOST)/dev/github-copilot-svcs:$(VERSION)`
- [x] 1.2 Add `docker-push` target: logs in to registry, tags image as `latest`, pushes both versioned and `latest` tags
- [x] 1.3 Add `release` target: chains `build docker-build docker-push git-commit-push` via dependency or explicit sequence
- [x] 1.4 Add `VERSION` variable defaulting to `$(shell date +%Y%m%d)-local` at top of Makefile

## 2. Release script

- [x] 2.1 Create `scripts/release.sh` with `set -euo pipefail`
- [x] 2.2 Validate required env vars (`DOCKER_REGISTRY_HOST`, `DOCKER_REGISTRY_USER`, `DOCKER_REGISTRY_PASSWORD`) — print named error and exit 1 if any missing
- [x] 2.3 Implement `DRY_RUN=1` mode: print each command with `[DRY-RUN]` prefix, skip `docker push`, `git commit`, `git push`
- [x] 2.4 Print `git status` summary before `git add -A && git commit -m "chore: release <VERSION>" && git push`
- [x] 2.5 Make script executable (`chmod +x scripts/release.sh`)

## 3. Documentation

- [x] 3.1 Update `README.md`: add "Local Release" section documenting `make release`, required env vars, `VERSION` override, and `DRY_RUN=1` usage

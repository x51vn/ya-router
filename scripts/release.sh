#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# release.sh — Local release workflow
#
# Usage:
#   ./scripts/release.sh
#   VERSION=v2.1.0 ./scripts/release.sh
#   DRY_RUN=1 ./scripts/release.sh
#
# Required env vars (unless DRY_RUN=1):
#   DOCKER_REGISTRY_HOST       e.g. docker.x51.vn
#   DOCKER_REGISTRY_USER       registry username
#   DOCKER_REGISTRY_PASSWORD   registry password
# ---------------------------------------------------------------------------

BINARY="github-copilot-svcs"
VERSION="${VERSION:-$(date +%Y%m%d)-local}"
DRY_RUN="${DRY_RUN:-0}"

# --- helpers ----------------------------------------------------------------

log() { echo "[release] $*"; }
dry_log() { echo "[DRY-RUN] would run: $*"; }

run() {
  if [[ "$DRY_RUN" == "1" ]]; then
    dry_log "$*"
  else
    log "running: $*"
    eval "$*"
  fi
}

# --- env validation ---------------------------------------------------------

if [[ "$DRY_RUN" != "1" ]]; then
  missing=()
  for var in DOCKER_REGISTRY_HOST DOCKER_REGISTRY_USER DOCKER_REGISTRY_PASSWORD; do
    if [[ -z "${!var:-}" ]]; then
      missing+=("$var")
    fi
  done
  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "[release] ERROR: missing required environment variable(s):" >&2
    for v in "${missing[@]}"; do
      echo "  - $v" >&2
    done
    echo "" >&2
    echo "  Set them before running, e.g.:" >&2
    echo "    export DOCKER_REGISTRY_HOST=docker.x51.vn" >&2
    exit 1
  fi
fi

# --- main -------------------------------------------------------------------

log "Starting release: VERSION=${VERSION}"
[[ "$DRY_RUN" == "1" ]] && log "(dry-run mode — no destructive operations will run)"

# 1. Go build
run "make build VERSION=${VERSION}"

# 2. Docker build
run "make docker-build VERSION=${VERSION}"

# 3. Docker push
run "make docker-push VERSION=${VERSION}"

# 4. Git commit + push
log "--- git status ---"
if [[ "$DRY_RUN" != "1" ]]; then
  git status
fi
run "git add -A"
run "git commit -m 'chore: release ${VERSION}'"
run "git push"

log "Release ${VERSION} complete."

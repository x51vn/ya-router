#!/bin/sh

set -eu

CONFIG_DIR="/home/appuser/.local/share/github-copilot-svcs"
CODEX_DIR="${CODEX_HOME:-/app/.codex}"

mkdir -p "$CONFIG_DIR" "$CODEX_DIR"

if [ "$(id -u)" = 0 ]; then
  chown -R appuser:appuser "$CONFIG_DIR" "$CODEX_DIR"
  chmod 0775 "$CONFIG_DIR"
  chmod 0700 "$CODEX_DIR"
  exec su-exec appuser /app/github-copilot-svcs "$@"
fi

exec /app/github-copilot-svcs "$@"

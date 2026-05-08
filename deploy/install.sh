#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYSTEMD_USER_DIR="${HOME}/.config/systemd/user"

if ! command -v systemctl &>/dev/null; then
    echo "WARNING: systemctl not found. This script requires systemd. Skipping install."
    exit 1
fi

mkdir -p "${SYSTEMD_USER_DIR}"

echo "Installing systemd user units..."
cp "${SCRIPT_DIR}"/systemd/otoxan-mcp-*.service "${SYSTEMD_USER_DIR}/"

echo "Reloading systemd daemon..."
systemctl --user daemon-reload

echo "Enabling and starting MCP servers..."
for svc in otoxan-mcp-tasks otoxan-mcp-plans otoxan-mcp-flows otoxan-mcp-memory otoxan-mcp-knowledge; do
    systemctl --user enable --now "${svc}"
done

echo "Done. Checking status..."
systemctl --user is-active otoxan-mcp-tasks otoxan-mcp-plans otoxan-mcp-flows otoxan-mcp-memory otoxan-mcp-knowledge || true

#!/usr/bin/env bash
# MCP stdio server wrapper — keeps stdin open so the server stays alive
# until systemd stops the service.

set -euo pipefail

BINARY="$1"
FIFO_DIR="${XDG_RUNTIME_DIR:-/tmp}/otoxan-mcp-$(basename "$BINARY")"
mkdir -p "$FIFO_DIR"
FIFO="$FIFO_DIR/stdin.fifo"

# Create FIFO if it doesn't exist
[ -p "$FIFO" ] || mkfifo "$FIFO"

# Keep the FIFO fed with an endless stream of newlines (harmless to JSON-RPC)
# This prevents EOF so the MCP server stays alive.
{ while true; do echo; sleep 60; done; } > "$FIFO" &
FEEDER_PID=$!

cleanup() {
    kill "$FEEDER_PID" 2>/dev/null || true
    rm -f "$FIFO"
}
trap cleanup EXIT

exec "$BINARY" < "$FIFO"

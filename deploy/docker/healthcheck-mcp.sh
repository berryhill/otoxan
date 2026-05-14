#!/bin/sh
# Health check wrapper for otoxan MCP servers.
# Tests Mongo connectivity by running the binary with a health check flag.
# Usage: healthcheck-mcp.sh <binary-path> [args...]
#
# The underlying binary should support --health-check flag which:
#   1. Attempts Mongo connection
#   2. Exits 0 on success, non-zero on failure

set -e

BINARY="${1:-}"
if [ -z "$BINARY" ]; then
    echo "usage: healthcheck <binary-path> [args...]" >&2
    exit 1
fi

shift

# Run the binary with health-check mode
# It should: connect to Mongo, ping, and exit 0 without serving stdio
exec "$BINARY" --health-check "$@"

#!/usr/bin/env bash
# test/quickstart_smoke.sh
# Smoke test: prove the README quickstart works on a fresh machine.
#
# Steps:
#   1. Start MongoDB in Docker
#   2. Build otoxan CLI from source
#   3. Run otoxan init non-interactively (env vars)
#   4. Run otoxan task list --agent test  → expect empty list, exit 0
#   5. Run otoxan task create --agent test --title "smoke"
#   6. Run otoxan task list --agent test  → expect 1 task
#
# Usage:
#   cd ~/code/otoxan/otoxan && bash test/quickstart_smoke.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

MONGO_CONTAINER_NAME="otoxan-smoke-mongo"
# Use an ephemeral port to avoid collisions with existing MongoDB instances.
MONGO_PORT=""
MONGO_URI=""
MONGO_DB="otoxan_smoke"

# ------------------------------------------------------------------
# Helpers
# ------------------------------------------------------------------

log() { echo "[smoke] $*"; }
fail() { echo "[smoke] FAIL: $*" >&2; exit 1; }

cleanup() {
    log "cleaning up..."
    if docker ps -q -f name="${MONGO_CONTAINER_NAME}" >/dev/null 2>&1; then
        docker rm -f "${MONGO_CONTAINER_NAME}" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

# ------------------------------------------------------------------
# 1. Start MongoDB
# ------------------------------------------------------------------

log "starting MongoDB container..."
MONGO_PORT=$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()")
MONGO_URI="mongodb://localhost:${MONGO_PORT}"

docker run -d \
    --name "${MONGO_CONTAINER_NAME}" \
    -p "${MONGO_PORT}:27017" \
    --health-cmd "mongosh --eval 'db.adminCommand({ ping: 1 })' || mongo --eval 'db.adminCommand({ ping: 1 })'" \
    --health-interval 2s \
    --health-timeout 3s \
    --health-retries 10 \
    mongo:7 > /dev/null

# Wait for MongoDB to accept connections
log "waiting for MongoDB..."
SECONDS=0
while (( SECONDS < 30 )); do
    if docker exec "${MONGO_CONTAINER_NAME}" mongosh --eval 'db.adminCommand({ ping: 1 })' >/dev/null 2>&1 || \
       docker exec "${MONGO_CONTAINER_NAME}" mongo --eval 'db.adminCommand({ ping: 1 })' >/dev/null 2>&1; then
        break
    fi
    sleep 1
done

if (( SECONDS >= 30 )); then
    fail "MongoDB did not become ready within 30s"
fi
log "MongoDB ready"

# ------------------------------------------------------------------
# 2. Build otoxan
# ------------------------------------------------------------------

log "building otoxan CLI..."
cd "${PROJECT_ROOT}"
go build -o /tmp/otoxan-smoke ./cmd/otoxan
OTOXAN="/tmp/otoxan-smoke"

# ------------------------------------------------------------------
# 3. Bootstrap config non-interactively via env vars
# ------------------------------------------------------------------

export OTOXAN_HOME="/tmp/otoxan-smoke-home"
export OTOXAN_MONGO_URI="${MONGO_URI}"
export OTOXAN_MONGO_DB="${MONGO_DB}"
export OTOXAN_DEFAULT_AGENT="smoke-agent"

rm -rf "${OTOXAN_HOME}"
mkdir -p "${OTOXAN_HOME}"

# Write a minimal config.yaml so we skip the interactive init prompt
cat > "${OTOXAN_HOME}/config.yaml" <<EOF
default_agent: smoke-agent
mongo_uri: "${MONGO_URI}"
mongo_db: ${MONGO_DB}
EOF

log "config written to ${OTOXAN_HOME}/config.yaml"

# ------------------------------------------------------------------
# 4. Verify connectivity — task list (empty)
# ------------------------------------------------------------------

log "running: otoxan task list --agent smoke-agent"
output="$(${OTOXAN} task list --agent smoke-agent 2>&1)" || fail "task list failed: ${output}"

# Expect empty JSON array
if ! echo "${output}" | grep -q '\[\]'; then
    fail "expected empty task list [], got: ${output}"
fi
log "task list returned empty array ✓"

# ------------------------------------------------------------------
# 5. Create a task
# ------------------------------------------------------------------

log "running: otoxan task create smoke-001 --title 'smoke' --assignee smoke-agent"
output="$(${OTOXAN} task create smoke-001 --title "smoke" --assignee smoke-agent 2>&1)" || fail "task create failed: ${output}"
log "task created ✓"

# ------------------------------------------------------------------
# 6. Verify task appears in list
# ------------------------------------------------------------------

log "running: otoxan task list --agent smoke-agent"
output="$(${OTOXAN} task list --agent smoke-agent 2>&1)" || fail "task list (second) failed: ${output}"

# Expect a non-empty array with our task
if echo "${output}" | grep -q '\[\]'; then
    fail "expected 1 task in list, got empty array"
fi
if ! echo "${output}" | grep -q '"task_id":\s*"smoke-001"'; then
    fail "expected task smoke-001 in list, got: ${output}"
fi
log "task list returned 1 task with smoke-001 ✓"

# ------------------------------------------------------------------
# 7. Verify task get
# ------------------------------------------------------------------

log "running: otoxan task get smoke-001"
output="$(${OTOXAN} task get smoke-001 2>&1)" || fail "task get failed: ${output}"
if ! echo "${output}" | grep -q '"task_id":\s*"smoke-001"'; then
    fail "expected task smoke-001 from get, got: ${output}"
fi
log "task get returned smoke-001 ✓"

# ------------------------------------------------------------------
# Done
# ------------------------------------------------------------------

log "ALL CHECKS PASSED ✓"

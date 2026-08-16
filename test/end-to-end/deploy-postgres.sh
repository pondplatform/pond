#!/usr/bin/env bash
# End-to-end scenario: deploy the postgres-demo service with a managed postgres dependency.
#
# Flow:
#   1. Submit deployment (no --wait) → get deployment ID
#   2. Wait for awaiting_input
#   3. Provide user input (managed=true, instances=1)
#   4. Wait for succeeded
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#     or POND_TOKEN, POND_PROJECT_ID, POND_SERVER_URL set in environment
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-postgres.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

# ── Submit ────────────────────────────────────────────────────────────────────
step "Submitting postgres-demo deployment (tag: $IMAGE_TAG)"
DEPLOY_OUTPUT=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  "$POND" deploy \
    --config "$SCRIPT_DIR/../test-data/postgres/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" 2>&1)
echo "$DEPLOY_OUTPUT"

DEPLOYMENT_ID=$(get_deployment_id "$DEPLOY_OUTPUT")
info "Deployment ID: $DEPLOYMENT_ID"

# ── Wait for awaiting_input ───────────────────────────────────────────────────
step "Waiting for deployment to reach awaiting_input"
poll_status "$DEPLOYMENT_ID" "awaiting_input" 30 2

# ── Provide user input ────────────────────────────────────────────────────────
step "Providing user input (managed=true, instances=1)"
INPUT_FILE=$(mktemp /tmp/pond-user-input.XXXXXX.json)
trap 'rm -f "$INPUT_FILE"' EXIT
cat > "$INPUT_FILE" <<'EOF'
{
  "dependencies": {
    "db": {
      "managed": true,
      "values": {
        "instances": 1
      }
    }
  }
}
EOF

POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  "$POND" deployment configure \
    --deployment-id "$DEPLOYMENT_ID" \
    --file "$INPUT_FILE"

# ── Wait for succeeded ────────────────────────────────────────────────────────
step "Waiting for deployment to complete"
poll_status "$DEPLOYMENT_ID" "succeeded" 150 2

step "Done — deployment succeeded"

#!/usr/bin/env bash
# End-to-end smoke test: deploys the postgres-demo service using the CLI.
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#     or POND_TOKEN, POND_ORG_ID, POND_PROJECT_ID, POND_SERVER_URL set in environment
#   - POND_CLUSTER_ID set to an existing cluster ID
#   - curl, jq, pond (CLI binary on PATH) installed
#
# Usage:
#   ./test/end-to-end/deploy-postgres.sh

set -euo pipefail

make build-cli

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source the token file when POND_TOKEN is not already in the environment.
E2E_ENV="$SCRIPT_DIR/.e2e-env"
if [[ -f "$E2E_ENV" && -z "${POND_TOKEN:-}" ]]; then
  # shellcheck source=test/end-to-end/.e2e-env
  source "$E2E_ENV"
fi

SERVER_URL="${POND_SERVER_URL:-http://localhost:8080}"
POND_TOKEN="${POND_TOKEN:?POND_TOKEN must be set (run local-setup.sh first)}"
PROJECT_ID="${POND_PROJECT_ID:?POND_PROJECT_ID must be set (run local-setup.sh first)}"
IMAGE_TAG="${POND_IMAGE_TAG:-latest}"
POND="${SCRIPT_DIR}/../../bin/pond"

bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "${yellow}  $*${reset}"; }
die()   { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; exit 1; }

dump_deployment_status() {
  local dep_id="${1:-}"
  [[ -n "$dep_id" ]] || return 0
  echo
  echo "${bold}Deployment status:${reset}"
  POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
    "$POND" deployment status --deployment-id "$dep_id" || true
}

# ── First deployment ──────────────────────────────────────────────────────────
step "Submitting postgres-demo deployment (tag: $IMAGE_TAG)"
DEPLOY_OUTPUT=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  "$POND" deploy \
    --config "$SCRIPT_DIR/../test-data/postgres/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" 2>&1)
echo "$DEPLOY_OUTPUT"

DEPLOYMENT_ID=$(echo "$DEPLOY_OUTPUT" | grep -oE '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' | head -1)
[[ -n "$DEPLOYMENT_ID" ]] || die "Could not parse deployment ID from output"
info "Deployment ID: $DEPLOYMENT_ID"

# ── Poll until awaiting_input ─────────────────────────────────────────────────
step "Waiting for deployment to reach awaiting_input status"
for i in $(seq 1 30); do
  STATUS=$(curl -sf -H "Authorization: Bearer $POND_TOKEN" \
    "$SERVER_URL/api/v1/deployments/$DEPLOYMENT_ID" | jq -r '.status')
  info "Status: $STATUS"
  if [[ "$STATUS" == "awaiting_input" ]]; then
    break
  elif [[ "$STATUS" == "failed" || "$STATUS" == "succeeded" ]]; then
    dump_deployment_status "$DEPLOYMENT_ID"
    die "Unexpected terminal status: $STATUS"
  fi
  sleep 2
done
if [[ "$STATUS" != "awaiting_input" ]]; then
  dump_deployment_status "$DEPLOYMENT_ID"
  die "Deployment did not reach awaiting_input (last status: $STATUS)"
fi

# ── Provide user input ────────────────────────────────────────────────────────
# The postgres provider requires provider_user_input.instances (number of
# PostgreSQL cluster instances to provision).
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

# ── Wait for deployment to complete ──────────────────────────────────────────
step "Waiting for deployment to complete"
for i in $(seq 1 150); do
  STATUS=$(curl -sf -H "Authorization: Bearer $POND_TOKEN" \
    "$SERVER_URL/api/v1/deployments/$DEPLOYMENT_ID" | jq -r '.status')
  info "Status: $STATUS"
  if [[ "$STATUS" == "succeeded" ]]; then
    break
  elif [[ "$STATUS" == "failed" ]]; then
    dump_deployment_status "$DEPLOYMENT_ID"
    die "Deployment failed"
  fi
  sleep 2
done
if [[ "$STATUS" != "succeeded" ]]; then
  dump_deployment_status "$DEPLOYMENT_ID"
  die "Deployment did not complete in time (last status: $STATUS)"
fi

step "Done — deployment succeeded"

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

bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
die()   { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; exit 1; }

# ── Deploy ────────────────────────────────────────────────────────────────────
step "Deploying postgres-demo (tag: $IMAGE_TAG)"
POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  $SCRIPT_DIR/../../bin/pond deploy deploy \
    --config "$SCRIPT_DIR/../test-data/postgres/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" \
    --wait

step "Done — deployment succeeded"

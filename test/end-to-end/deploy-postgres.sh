#!/usr/bin/env bash
# End-to-end scenario: deploy the postgres-demo service with a managed postgres dependency.
#
# Flow:
#   1. Submit the deployment with its dependency input (managed=true, instances=1)
#   2. Wait for succeeded: tofu provisioned the database, and the readiness probe
#      only passes once the app has connected to it with the rendered credentials
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
    --pond-file "$SCRIPT_DIR/../test-data/postgres/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" \
    --input "$SCRIPT_DIR/../test-data/postgres/postgres-deploy-input.json" 2>&1)
echo "$DEPLOY_OUTPUT"

DEPLOYMENT_ID=$(get_deployment_id "$DEPLOY_OUTPUT")
info "Deployment ID: $DEPLOYMENT_ID"

# ── Wait for succeeded ────────────────────────────────────────────────────────
step "Waiting for deployment to complete"
poll_status "$DEPLOYMENT_ID" "succeeded" 150 2

step "Done — deployment succeeded"

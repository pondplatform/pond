#!/usr/bin/env bash
# End-to-end smoke test: creates a project + environment and deploys the
# postgres-demo service from test/test-data/postgres/pond.yml using the CLI.
#
# Prerequisites:
#   - pond-server running (POND_SERVER_URL, default: http://localhost:8080)
#   - test/end-to-end/.e2e-env present (run scripts/create-org.sh first)
#     or POND_TOKEN, POND_ORG_ID, POND_SERVER_URL set in the environment
#   - POND_CLUSTER_ID set to an existing cluster ID
#   - curl, jq, pond (CLI binary on PATH) installed
#
# Usage:
#   POND_CLUSTER_ID=<uuid> ./test/end-to-end/deploy-postgres.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source the token file when POND_TOKEN is not already in the environment.
E2E_ENV="$SCRIPT_DIR/.e2e-env"
if [[ -f "$E2E_ENV" && -z "${POND_TOKEN:-}" ]]; then
  # shellcheck source=test/end-to-end/.e2e-env
  source "$E2E_ENV"
fi

SERVER_URL="${POND_SERVER_URL:-http://localhost:8080}"
POND_TOKEN="${POND_TOKEN:?POND_TOKEN must be set (run scripts/create-org.sh first)}"
POND_ORG_ID="${POND_ORG_ID:?POND_ORG_ID must be set (run scripts/create-org.sh first)}"
CLUSTER_ID="${POND_CLUSTER_ID:?POND_CLUSTER_ID must be set}"
IMAGE_TAG="${POND_IMAGE_TAG:-latest}"

bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "  ${yellow}$*${reset}"; }
die()   { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; exit 1; }

curl_api() {
  local desc="$1"; shift
  local tmp http_code body
  tmp=$(mktemp)
  http_code=$(curl -s -o "$tmp" -w "%{http_code}" "$@") || {
    rm -f "$tmp"; die "$desc: curl failed"
  }
  body=$(cat "$tmp"); rm -f "$tmp"
  [[ "$http_code" =~ ^2 ]] || die "$desc failed (HTTP $http_code): $body"
  printf '%s' "$body"
}

# ── 1. Create project (idempotent) ────────────────────────────────────────────
step "Creating project 'e2e-project'"
tmpfile=$(mktemp)
PROJ_HTTP=$(curl -s -o "$tmpfile" -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/organizations/$POND_ORG_ID/projects" \
  -H "Authorization: Bearer $POND_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"e2e-project"}')
PROJ_BODY=$(cat "$tmpfile"); rm -f "$tmpfile"

if [[ "$PROJ_HTTP" == "201" ]]; then
  PROJECT_ID=$(printf '%s' "$PROJ_BODY" | jq -r '.id')
elif [[ "$PROJ_HTTP" == "409" ]]; then
  info "Project already exists — looking it up"
  LIST=$(curl_api "GET /organizations/$POND_ORG_ID/projects" \
    "$SERVER_URL/api/v1/organizations/$POND_ORG_ID/projects" \
    -H "Authorization: Bearer $POND_TOKEN")
  PROJECT_ID=$(printf '%s' "$LIST" | jq -r '.items[] | select(.name=="e2e-project") | .id')
else
  die "POST /organizations/$POND_ORG_ID/projects failed (HTTP $PROJ_HTTP): $PROJ_BODY"
fi
[[ -n "$PROJECT_ID" && "$PROJECT_ID" != "null" ]] || die "could not determine project ID"
info "Project ID: $PROJECT_ID"

# ── 2. Create environment (idempotent) ────────────────────────────────────────
step "Creating environment 'staging'"
tmpfile=$(mktemp)
ENV_HTTP=$(curl -s -o "$tmpfile" -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/projects/$PROJECT_ID/environments" \
  -H "Authorization: Bearer $POND_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"staging\",\"namespace\":\"e2e-staging\",\"clusterId\":\"$CLUSTER_ID\"}")
ENV_BODY=$(cat "$tmpfile"); rm -f "$tmpfile"

if [[ "$ENV_HTTP" == "201" ]]; then
  info "Environment created"
elif [[ "$ENV_HTTP" == "409" ]]; then
  info "Environment already exists"
else
  die "POST /projects/$PROJECT_ID/environments failed (HTTP $ENV_HTTP): $ENV_BODY"
fi

# ── 3. Deploy ─────────────────────────────────────────────────────────────────
step "Deploying postgres-demo (tag: $IMAGE_TAG)"
POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  pond deploy \
    --config "$SCRIPT_DIR/../test-data/postgres/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" \
    --wait

step "Done — deployment succeeded"

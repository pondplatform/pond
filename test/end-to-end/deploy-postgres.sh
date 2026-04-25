#!/usr/bin/env bash
# End-to-end smoke test: creates an org, project, environment, and deploys the
# postgres-demo service from test/test-data/postgres/pond.yml.
#
# Prerequisites:
#   - pond-server running at POND_SERVER_URL (default: http://localhost:8080)
#   - POND_ADMIN_KEY set to the server's admin key
#   - POND_CLUSTER_ID set to an existing cluster ID
#   - curl, jq installed
#
# Usage:
#   POND_ADMIN_KEY=<key> POND_CLUSTER_ID=<uuid> ./test/end-to-end/deploy-postgres.sh

set -euo pipefail

SERVER_URL="${POND_SERVER_URL:-http://localhost:8080}"
ADMIN_KEY="${POND_ADMIN_KEY:?POND_ADMIN_KEY must be set}"
CLUSTER_ID="${POND_CLUSTER_ID:?POND_CLUSTER_ID must be set}"
IMAGE_TAG="${POND_IMAGE_TAG:-latest}"

bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "  ${yellow}$*${reset}"; }
die()   { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; exit 1; }

api() {
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

# ── 1. Create organisation ────────────────────────────────────────────────────
step "Creating organisation 'e2e-org'"
ORG_HTTP=$(curl -s -o /tmp/pond_e2e_org -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/organizations" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"e2e-org"}')
ORG_BODY=$(cat /tmp/pond_e2e_org)

if [[ "$ORG_HTTP" == "201" ]]; then
  ORG_ID=$(echo "$ORG_BODY" | jq -r '.id')
elif [[ "$ORG_HTTP" == "409" ]]; then
  info "Organisation already exists — looking it up"
  LIST=$(api "GET /organizations" \
    "$SERVER_URL/api/v1/organizations" \
    -H "Authorization: Bearer $ADMIN_KEY")
  ORG_ID=$(echo "$LIST" | jq -r '.items[] | select(.name=="e2e-org") | .id')
else
  die "POST /organizations failed (HTTP $ORG_HTTP): $ORG_BODY"
fi
[[ -n "$ORG_ID" && "$ORG_ID" != "null" ]] || die "could not determine org ID"
info "Org ID: $ORG_ID"

# ── 2. Create API token for the org ──────────────────────────────────────────
step "Creating API token"
TOKEN_BODY=$(api "POST /organizations/$ORG_ID/tokens" \
  -X POST "$SERVER_URL/api/v1/organizations/$ORG_ID/tokens" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"role":"admin","description":"e2e-test"}')
ORG_TOKEN=$(echo "$TOKEN_BODY" | jq -r '.token')
[[ -n "$ORG_TOKEN" && "$ORG_TOKEN" != "null" ]] || die "could not obtain org token"
info "Token obtained"

# ── 3. Create project ─────────────────────────────────────────────────────────
step "Creating project 'e2e-project'"
PROJ_HTTP=$(curl -s -o /tmp/pond_e2e_proj -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/organizations/$ORG_ID/projects" \
  -H "Authorization: Bearer $ORG_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"e2e-project"}')
PROJ_BODY=$(cat /tmp/pond_e2e_proj)

if [[ "$PROJ_HTTP" == "201" ]]; then
  PROJECT_ID=$(echo "$PROJ_BODY" | jq -r '.id')
elif [[ "$PROJ_HTTP" == "409" ]]; then
  info "Project already exists — looking it up"
  LIST=$(api "GET /organizations/$ORG_ID/projects" \
    "$SERVER_URL/api/v1/organizations/$ORG_ID/projects" \
    -H "Authorization: Bearer $ORG_TOKEN")
  PROJECT_ID=$(echo "$LIST" | jq -r '.items[] | select(.name=="e2e-project") | .id')
else
  die "POST /organizations/$ORG_ID/projects failed (HTTP $PROJ_HTTP): $PROJ_BODY"
fi
[[ -n "$PROJECT_ID" && "$PROJECT_ID" != "null" ]] || die "could not determine project ID"
info "Project ID: $PROJECT_ID"

# ── 4. Create environment ─────────────────────────────────────────────────────
step "Creating environment 'staging'"
ENV_HTTP=$(curl -s -o /tmp/pond_e2e_env -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/projects/$PROJECT_ID/environments" \
  -H "Authorization: Bearer $ORG_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"staging\",\"namespace\":\"e2e-staging\",\"clusterId\":\"$CLUSTER_ID\"}")
ENV_BODY=$(cat /tmp/pond_e2e_env)

if [[ "$ENV_HTTP" == "201" ]]; then
  info "Environment created"
elif [[ "$ENV_HTTP" == "409" ]]; then
  info "Environment already exists"
else
  die "POST /projects/$PROJECT_ID/environments failed (HTTP $ENV_HTTP): $ENV_BODY"
fi

# ── 5. Submit deployment ──────────────────────────────────────────────────────
step "Submitting deployment (tag: $IMAGE_TAG)"
DEPLOY_BODY=$(api "POST /deployments" \
  -X POST "$SERVER_URL/api/v1/deployments" \
  -H "Authorization: Bearer $ORG_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"ProjectID\": \"$PROJECT_ID\",
    \"EnvironmentName\": \"staging\",
    \"ImageTag\": \"$IMAGE_TAG\",
    \"TriggeredBy\": \"e2e-script\",
    \"CreateIfNotExists\": true,
    \"OverridableConfig\": {
      \"version\": 1,
      \"name\": \"postgres-demo\",
      \"image\": \"ghcr.io/pondplatform/postgres-demo\",
      \"service\": {\"port\": 3000},
      \"dependencies\": {
        \"db\": {\"type\": \"postgres\"}
      },
      \"configs\": {
        \".env\": {
          \"format\": \".env\",
          \"mountDir\": \"/app/\",
          \"values\": {
            \"DB_HOST\": \"{{db.host}}\",
            \"DB_PORT\": \"{{db.port}}\",
            \"DB_USER\": \"{{db.username}}\",
            \"DB_PASSWORD\": \"{{db.password}}\",
            \"DB_NAME\": \"{{db.database}}\"
          }
        }
      }
    }
  }")

DEPLOYMENT_ID=$(echo "$DEPLOY_BODY" | jq -r '.id')
DEPLOYMENT_STATUS=$(echo "$DEPLOY_BODY" | jq -r '.status')
[[ -n "$DEPLOYMENT_ID" && "$DEPLOYMENT_ID" != "null" ]] || die "could not determine deployment ID"
info "Deployment ID: $DEPLOYMENT_ID"
info "Initial status: $DEPLOYMENT_STATUS"

# ── 6. Poll until complete ────────────────────────────────────────────────────
step "Waiting for deployment to complete"
for i in $(seq 1 60); do
  STATUS_BODY=$(api "GET /deployments/$DEPLOYMENT_ID" \
    "$SERVER_URL/api/v1/deployments/$DEPLOYMENT_ID" \
    -H "Authorization: Bearer $ORG_TOKEN")
  STATUS=$(echo "$STATUS_BODY" | jq -r '.status')
  info "attempt $i — status: $STATUS"
  case "$STATUS" in
    succeeded) break ;;
    failed)    die "deployment $DEPLOYMENT_ID failed" ;;
    *)         sleep 3 ;;
  esac
done
[[ "$STATUS" == "succeeded" ]] || die "deployment did not complete within timeout"

step "Done — deployment $DEPLOYMENT_ID succeeded"

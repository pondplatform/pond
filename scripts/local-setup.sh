#!/usr/bin/env bash
# Sets up pond-server + pond-agent on Rancher Desktop for local manual testing.
# Run from the repo root: ./scripts/local-setup.sh
#
# What it does:
#   1. Builds pond-server and pond-agent Docker images
#   2. Imports them into the Rancher Desktop containerd runtime (rdctl)
#   3. Installs the pond-server Helm chart (postgres + rabbitmq included)
#   4. Waits for the server to be healthy, then port-forwards it
#   5. Creates an org + cluster via the API using the admin key
#   6. Installs the pond-agent Helm chart with the cluster's agent token
#
# Prerequisites:
#   - Rancher Desktop running with the "rancher-desktop" kube context active
#   - kubectl, helm, curl, jq installed

set -euo pipefail

NAMESPACE="${NAMESPACE:-pond}"
SERVER_RELEASE="${SERVER_RELEASE:-pond-server}"
AGENT_RELEASE="${AGENT_RELEASE:-pond-agent}"
SERVER_PORT="${SERVER_PORT:-8080}"
CONTEXT="rancher-desktop"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HELM_SERVER="$REPO_ROOT/infra/deploy/helm/pond-server"
HELM_AGENT="$REPO_ROOT/infra/deploy/helm/pond-agent"

LOG_FILE="/tmp/pond-local-setup-$(date +%Y%m%d-%H%M%S).log"
exec > >(tee -a "$LOG_FILE") 2>&1

# ── Colours ───────────────────────────────────────────────────────────────────
bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "  ${yellow}$*${reset}"; }
error() { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; }
die()   { error "$*"; exit 1; }

# curl_api <desc> [curl-args…]  — prints response body and dies on non-2xx or curl error
curl_api() {
  local desc="$1"; shift
  local tmpfile http_code body
  tmpfile=$(mktemp)
  http_code=$(curl -s -o "$tmpfile" -w "%{http_code}" "$@") \
    || { body=$(cat "$tmpfile"); rm -f "$tmpfile"; die "$desc: curl error — $body"; }
  body=$(cat "$tmpfile"); rm -f "$tmpfile"
  if [[ ! "$http_code" =~ ^2 ]]; then
    error "$desc failed (HTTP $http_code)"
    error "Response: $body"
    exit 1
  fi
  printf '%s' "$body"
}

info "Logging to $LOG_FILE"

# ── Sanity checks ─────────────────────────────────────────────────────────────
for cmd in kubectl helm curl jq rdctl; do
  command -v "$cmd" &>/dev/null || die "$cmd not found in PATH"
done

CURRENT_CTX=$(kubectl config current-context 2>/dev/null) \
  || die "kubectl could not read the current context — is kubeconfig set up?"
[[ "$CURRENT_CTX" == "$CONTEXT" ]] \
  || die "current kube context is '$CURRENT_CTX', expected '$CONTEXT' — run: kubectl config use-context $CONTEXT"

# ── 1. Build images ───────────────────────────────────────────────────────────
step "Building Docker images"
docker build -f "$REPO_ROOT/infra/docker/Dockerfile.server" -t pond-server:latest "$REPO_ROOT" \
  || die "docker build failed for pond-server"
docker build -f "$REPO_ROOT/infra/docker/Dockerfile.agent"  -t pond-agent:latest  "$REPO_ROOT" \
  || die "docker build failed for pond-agent"



# ── 3. Generate secrets ───────────────────────────────────────────────────────
step "Generating secrets"
JWT_SECRET=$(openssl rand -base64 32) || die "openssl failed to generate JWT secret"
ADMIN_KEY=$(openssl rand -base64 24)  || die "openssl failed to generate admin key"
info "JWT secret and admin key generated (not shown)"

# ── 4. Install pond-server chart ──────────────────────────────────────────────
step "Installing pond-server chart into namespace '$NAMESPACE'"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - \
  || die "failed to create namespace '$NAMESPACE'"

helm upgrade --install "$SERVER_RELEASE" "$HELM_SERVER" \
  --namespace "$NAMESPACE" \
  --kube-context "$CONTEXT" \
  --set image.repository=pond-server \
  --set image.tag=latest \
  --set image.pullPolicy=Never \
  --set jwtSecret="$JWT_SECRET" \
  --set adminKey="$ADMIN_KEY" \
  --wait --timeout 3m \
  || die "helm upgrade failed for pond-server (check: kubectl logs -n $NAMESPACE -l app=$SERVER_RELEASE)"

# ── 5. Wait for server pod + port-forward ─────────────────────────────────────
step "Port-forwarding pond-server to localhost:$SERVER_PORT"
# Kill any stale port-forward
pkill -f "kubectl port-forward.*$SERVER_PORT" 2>/dev/null || true

kubectl port-forward \
  --namespace "$NAMESPACE" \
  --context "$CONTEXT" \
  "svc/$SERVER_RELEASE" \
  "${SERVER_PORT}:8080" &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT

# Wait until the server responds
SERVER_URL="http://localhost:$SERVER_PORT"
info "Waiting for server at $SERVER_URL ..."
SERVER_READY=0
for i in $(seq 1 30); do
  if curl -sf -o /dev/null "$SERVER_URL/api/v1/organizations" \
      -H "Authorization: Bearer $ADMIN_KEY"; then
    SERVER_READY=1
    break
  fi
  info "attempt $i/30 — retrying in 2s"
  sleep 2
done
[[ $SERVER_READY -eq 1 ]] || die "server did not become healthy after 60s (port-forward PID $PF_PID — check: kubectl logs -n $NAMESPACE -l app=$SERVER_RELEASE)"

# ── 6. Create organisation (idempotent) ───────────────────────────────────────
step "Creating organisation 'local-org'"
ORG_HTTP=$(curl -s -o /tmp/pond_org_body -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/organizations" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"local-org"}')
ORG_BODY=$(cat /tmp/pond_org_body)
if [[ "$ORG_HTTP" == "201" ]]; then
  ORG_ID=$(echo "$ORG_BODY" | jq -r '.id')
elif [[ "$ORG_HTTP" == "409" ]]; then
  info "Organisation already exists — looking it up"
  LIST_RESPONSE=$(curl_api "GET /organizations" \
    "$SERVER_URL/api/v1/organizations" \
    -H "Authorization: Bearer $ADMIN_KEY")
  ORG_ID=$(echo "$LIST_RESPONSE" | jq -r '.items[] | select(.name=="local-org") | .id')
else
  error "POST /organizations failed (HTTP $ORG_HTTP)"
  error "Response: $ORG_BODY"
  exit 1
fi
[[ -n "$ORG_ID" && "$ORG_ID" != "null" ]] || die "could not determine org ID"
info "Organisation ID: $ORG_ID"

# ── 7. Create cluster + obtain agent token (idempotent) ───────────────────────
step "Creating cluster 'rancher-desktop' in org"
CLUSTER_HTTP=$(curl -s -o /tmp/pond_cluster_body -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/organizations/$ORG_ID/clusters" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"rancher-desktop"}')
CLUSTER_BODY=$(cat /tmp/pond_cluster_body)
if [[ "$CLUSTER_HTTP" == "201" ]]; then
  CLUSTER_ID=$(echo "$CLUSTER_BODY"  | jq -r '.id')
  AGENT_TOKEN=$(echo "$CLUSTER_BODY" | jq -r '.agentToken')
elif [[ "$CLUSTER_HTTP" == "409" ]]; then
  info "Cluster already exists — looking it up and rotating token"
  LIST_RESPONSE=$(curl_api "GET /organizations/$ORG_ID/clusters" \
    "$SERVER_URL/api/v1/organizations/$ORG_ID/clusters" \
    -H "Authorization: Bearer $ADMIN_KEY")
  CLUSTER_ID=$(echo "$LIST_RESPONSE" | jq -r '.items[] | select(.name=="rancher-desktop") | .id')
  [[ -n "$CLUSTER_ID" && "$CLUSTER_ID" != "null" ]] || die "could not find cluster ID in: $LIST_RESPONSE"
  ROTATE_RESPONSE=$(curl_api "POST /organizations/$ORG_ID/clusters/$CLUSTER_ID/rotate-token" \
    -X POST "$SERVER_URL/api/v1/organizations/$ORG_ID/clusters/$CLUSTER_ID/rotate-token" \
    -H "Authorization: Bearer $ADMIN_KEY")
  AGENT_TOKEN=$(echo "$ROTATE_RESPONSE" | jq -r '.agentToken')
else
  error "POST /organizations/$ORG_ID/clusters failed (HTTP $CLUSTER_HTTP)"
  error "Response: $CLUSTER_BODY"
  exit 1
fi
[[ -n "$CLUSTER_ID"  && "$CLUSTER_ID"  != "null" ]] || die "could not determine cluster ID"
[[ -n "$AGENT_TOKEN" && "$AGENT_TOKEN" != "null" ]] || die "could not determine agent token"
info "Cluster ID:   $CLUSTER_ID"
info "Agent token:  $AGENT_TOKEN"

# ── 8. Install pond-agent chart ───────────────────────────────────────────────
step "Installing pond-agent chart"
helm upgrade --install "$AGENT_RELEASE" "$HELM_AGENT" \
  --namespace "$NAMESPACE" \
  --kube-context "$CONTEXT" \
  --set image.repository=pond-agent \
  --set image.tag=latest \
  --set image.pullPolicy=Never \
  --set serverAddr="${SERVER_RELEASE}.${NAMESPACE}.svc.cluster.local:8080" \
  --set agentToken="$AGENT_TOKEN" \
  --wait --timeout 2m \
  || die "helm upgrade failed for pond-agent (check: kubectl logs -n $NAMESPACE -l app=$AGENT_RELEASE)"

# ── 9. Summary ────────────────────────────────────────────────────────────────
step "Done"
echo
echo "${bold}Environment:${reset}"
echo "  Server URL:    $SERVER_URL"
echo "  Admin key:     $ADMIN_KEY"
echo "  JWT secret:    $JWT_SECRET"
echo "  Org ID:        $ORG_ID"
echo "  Cluster ID:    $CLUSTER_ID"
echo
echo "${bold}Useful commands:${reset}"
echo "  # Create a JWT token for the org:"
echo "  curl -sf -X POST $SERVER_URL/api/v1/organizations/$ORG_ID/tokens \\"
echo "    -H 'Authorization: Bearer $ADMIN_KEY' \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"role\":\"admin\",\"description\":\"local-dev\"}'"
echo
echo "  # Export for the CLI:"
echo "  export POND_SERVER_URL=$SERVER_URL"
echo
echo "  # Tear down:"
echo "  helm uninstall $SERVER_RELEASE $AGENT_RELEASE -n $NAMESPACE"
echo "  kubectl delete namespace $NAMESPACE"
echo
echo "  Port-forward PID $PF_PID is running. Press Ctrl-C to stop it."
# Keep port-forward alive until the user interrupts
wait $PF_PID

#!/usr/bin/env bash
# Installs pond-server + pond-agent into a Kubernetes cluster, creates a default project
# with environments, and configures the pond CLI context.
#
# Intended to be run directly or piped to bash:
#   curl -fsSL https://raw.githubusercontent.com/pondplatform/pond/main/scripts/setup.sh | bash
#   curl -fsSL ... | bash -s -- --kube-context my-cluster --environments staging,production
#
# Prerequisites: kubectl, helm, curl, jq, openssl, pond (or ./build/pond or go)

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
KUBE_CONTEXT=""
NAMESPACE="pond"
CONTEXT_NAME=""
CLUSTER_NAME=""
IMAGE_TAG="v0.2.1"
CHART_VERSION=""        # empty = latest published chart
FORWARD_PORT=""         # empty = pick a random free local port
ENVIRONMENTS=""         # empty = prompt or default to "production"
INSTALL_PROVIDERS=""    # empty = prompt; "true"/"false" if set via flag

SERVER_RELEASE="pond-server"
AGENT_RELEASE="pond-agent"
GHCR_CHARTS="oci://ghcr.io/pondplatform/charts"
GHCR_IMAGES="ghcr.io/pondplatform"

# ── Colours ───────────────────────────────────────────────────────────────────
bold=$'\033[1m'; reset=$'\033[0m'
green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'; cyan=$'\033[36m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "  ${yellow}$*${reset}"; }
error() { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; }
die()   { error "$*"; exit 1; }

usage() {
  cat <<EOF
Usage: $0 [OPTIONS]

Options:
  --kube-context <ctx>       kubectl context for the target cluster (prompted if omitted)
  --namespace <ns>           Kubernetes namespace              (default: pond)
  --context-name <name>      Pond CLI context name             (default: <kube-context>)
  --cluster-name <name>      Pond cluster registration name    (default: <kube-context>)
  --image-tag <tag>          Docker image tag                  (default: latest)
  --chart-version <ver>      Helm chart version                (default: latest published)
  --kubectl-forward-port <p> Local port for the port-forward   (default: random free port)
  --environments <list>      Comma-separated environment names (prompted if omitted)
  --default-providers        Install CloudNativePG operator    (prompted if omitted)
  --help                     Show this help
EOF
}

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --kube-context)      KUBE_CONTEXT="$2";      shift 2 ;;
    --namespace)         NAMESPACE="$2";          shift 2 ;;
    --context-name)      CONTEXT_NAME="$2";       shift 2 ;;
    --cluster-name)      CLUSTER_NAME="$2";       shift 2 ;;
    --image-tag)         IMAGE_TAG="$2";           shift 2 ;;
    --chart-version)     CHART_VERSION="$2";       shift 2 ;;
    --kubectl-forward-port) FORWARD_PORT="$2";     shift 2 ;;
    --environments)      ENVIRONMENTS="$2";        shift 2 ;;
    --default-providers) INSTALL_PROVIDERS="true"; shift ;;
    --help|-h)           usage; exit 0 ;;
    *) die "Unknown argument: $1 (run with --help for usage)" ;;
  esac
done

# ── Interactive prompting (reads from /dev/tty so piped installs work) ────────

# prompt_select <prompt> <default> <option1> [option2 ...] — prints chosen value
prompt_select() {
  local prompt="$1" default="$2"; shift 2
  local options=("$@")
  local tty=/dev/tty

  echo "${bold}${cyan}${prompt}${reset}" >&"$tty"
  local i=1
  for opt in "${options[@]}"; do
    if [[ "$opt" == "$default" ]]; then
      printf '  %d) %s %s\n' "$i" "$opt" "${bold}(default)${reset}" >&"$tty"
    else
      printf '  %d) %s\n' "$i" "$opt" >&"$tty"
    fi
    (( i++ ))
  done

  local choice default_idx=1
  for j in "${!options[@]}"; do
    [[ "${options[$j]}" == "$default" ]] && default_idx=$(( j + 1 ))
  done

  while true; do
    printf '%s [%d]: ' "Select" "$default_idx" >&"$tty"
    read -r choice <"$tty"
    choice="${choice:-$default_idx}"
    if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#options[@]} )); then
      printf '%s' "${options[$(( choice - 1 ))]}"
      return 0
    fi
    echo "  Please enter a number between 1 and ${#options[@]}." >&"$tty"
  done
}

# prompt_input <prompt> <default> — prints entered value (or default)
prompt_input() {
  local prompt="$1" default="$2"
  local tty=/dev/tty
  local value
  if [[ -n "$default" ]]; then
    printf '%s [%s]: ' "${bold}${cyan}${prompt}${reset}" "$default" >&"$tty"
  else
    printf '%s: ' "${bold}${cyan}${prompt}${reset}" >&"$tty"
  fi
  read -r value <"$tty"
  printf '%s' "${value:-$default}"
}

# prompt_yesno <prompt> <default y|n> — sets variable to true/false
prompt_yesno() {
  local prompt="$1" default="$2"
  local tty=/dev/tty hint
  [[ "$default" == "y" ]] && hint="Y/n" || hint="y/N"
  printf '%s [%s]: ' "${bold}${cyan}${prompt}${reset}" "$hint" >&"$tty"
  local ans
  read -r ans <"$tty"
  ans="${ans:-$default}"
  case "$(printf '%s' "$ans" | tr '[:upper:]' '[:lower:]')" in
    y|yes) printf 'true'  ;;
    *)     printf 'false' ;;
  esac
}

# Prompt for kube context if not set
if [[ -z "$KUBE_CONTEXT" ]]; then
  echo >&/dev/tty
  echo "${bold}Pond Setup${reset}" >&/dev/tty
  echo "──────────────────────────────────────────────────" >&/dev/tty

  CONTEXTS=()
  while IFS= read -r _ctx; do CONTEXTS+=("$_ctx"); done \
    < <(kubectl config get-contexts -o name 2>/dev/null | sort)
  [[ ${#CONTEXTS[@]} -gt 0 ]] || die "No kubectl contexts found. Configure kubeconfig first."

  CURRENT_CTX=$(kubectl config current-context 2>/dev/null || true)
  DEFAULT_CTX="${CURRENT_CTX:-${CONTEXTS[0]}}"

  KUBE_CONTEXT=$(prompt_select "Which kubectl context should pond be installed into?" \
    "$DEFAULT_CTX" "${CONTEXTS[@]}")
  echo >&/dev/tty
fi

# Prompt for environments if not set
if [[ -z "$ENVIRONMENTS" ]]; then
  ENVIRONMENTS=$(prompt_input "Environments to create (comma-separated)" "production")
  echo >&/dev/tty
fi

# Prompt for default providers if not set
if [[ -z "$INSTALL_PROVIDERS" ]]; then
  INSTALL_PROVIDERS=$(prompt_yesno "Install default providers (CloudNativePG for managed Postgres)?" "n")
  echo >&/dev/tty
fi

# random_free_port — prints a local port in 20000-29999 that nothing is listening on
random_free_port() {
  local port
  for _ in $(seq 1 50); do
    port=$(( ( RANDOM % 10000 ) + 20000 ))
    if ! (exec 3<>"/dev/tcp/127.0.0.1/${port}") >/dev/null 2>&1; then
      printf '%s' "$port"
      return 0
    fi
  done
  die "could not find a free local port for the port-forward"
}

# Derive remaining defaults
CONTEXT_NAME="${CONTEXT_NAME:-$KUBE_CONTEXT}"
CLUSTER_NAME="${CLUSTER_NAME:-$KUBE_CONTEXT}"
FORWARD_PORT="${FORWARD_PORT:-$(random_free_port)}"
SERVER_URL="http://localhost:${FORWARD_PORT}"

# ── Helm version flag (empty = latest) ───────────────────────────────────────
HELM_VERSION_FLAG=()
if [[ -n "$CHART_VERSION" ]]; then
  HELM_VERSION_FLAG=(--version "$CHART_VERSION")
fi

# ── curl helper ───────────────────────────────────────────────────────────────
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

# ── Cleanup on exit ───────────────────────────────────────────────────────────
TMPFILES=()
cleanup() {
  for f in ${TMPFILES[@]+"${TMPFILES[@]}"}; do
    rm -f "$f" 2>/dev/null || true
  done
}
trap cleanup EXIT

# ── 1. Prerequisites ──────────────────────────────────────────────────────────
step "Checking prerequisites"

for cmd in kubectl helm curl jq openssl; do
  command -v "$cmd" &>/dev/null || die "$cmd not found in PATH"
done

kubectl config get-contexts "$KUBE_CONTEXT" --no-headers &>/dev/null \
  || die "kubectl context '$KUBE_CONTEXT' not found — check: kubectl config get-contexts"

if command -v pond &>/dev/null; then
  POND_CLI="pond"
elif [[ -x "./build/pond" ]]; then
  POND_CLI="./build/pond"
elif command -v go &>/dev/null; then
  info "pond CLI not found — building from source..."
  go build -o ./build/pond ./cli/ \
    || die "build failed — run: make build-cli"
  POND_CLI="./build/pond"
else
  die "pond CLI not found in PATH or ./build/pond. Install it or run: make build-cli"
fi

info "kubectl context: $KUBE_CONTEXT"
info "namespace:       $NAMESPACE"
info "chart version:   ${CHART_VERSION:-latest}"
info "image tag:       $IMAGE_TAG"
info "pond CLI:        $POND_CLI"

# ── 2. Generate secrets ───────────────────────────────────────────────────────
step "Generating secrets"
JWT_SECRET=$(openssl rand -base64 32) || die "openssl failed"
ADMIN_KEY=$(openssl rand -base64 24)  || die "openssl failed"
info "Admin key and JWT secret generated"

# ── 3. Install pond-server ────────────────────────────────────────────────────
step "Installing pond-server${CHART_VERSION:+ v${CHART_VERSION}} into namespace '$NAMESPACE'"

# Write secrets to a temp values file so base64 special chars don't confuse --set
VALS_FILE=$(mktemp /tmp/pond-values.XXXXXX.yaml)
TMPFILES+=("$VALS_FILE")
chmod 600 "$VALS_FILE"
printf 'jwtSecret: "%s"\nadminKey: "%s"\n' \
  "$(printf '%s' "$JWT_SECRET" | sed 's/"/\\"/g')" \
  "$(printf '%s' "$ADMIN_KEY"  | sed 's/"/\\"/g')" \
  > "$VALS_FILE"

helm upgrade --install "$SERVER_RELEASE" \
  "${GHCR_CHARTS}/${SERVER_RELEASE}" \
  ${HELM_VERSION_FLAG[@]+"${HELM_VERSION_FLAG[@]}"} \
  --kube-context "$KUBE_CONTEXT" \
  --namespace "$NAMESPACE" \
  --create-namespace \
  --set image.repository="${GHCR_IMAGES}/pond-server" \
  --set image.tag="$IMAGE_TAG" \
  --values "$VALS_FILE" \
  --wait --timeout 3m \
  || die "pond-server Helm install failed (logs: kubectl logs -n $NAMESPACE -l app=$SERVER_RELEASE --context $KUBE_CONTEXT)"

# ── 4. (Optional) Install CloudNativePG ──────────────────────────────────────
if [[ "$INSTALL_PROVIDERS" == "true" ]]; then
  step "Installing CloudNativePG operator (default providers)"
  helm repo add cnpg https://cloudnative-pg.github.io/charts --force-update 2>/dev/null || true
  helm upgrade --install cnpg cnpg/cloudnative-pg \
    --kube-context "$KUBE_CONTEXT" \
    --namespace cnpg-system \
    --create-namespace \
    --wait --timeout 3m \
    || die "CloudNativePG Helm install failed"
fi

# ── 5. Port-forward + wait for health ─────────────────────────────────────────
step "Port-forwarding pond-server to localhost:${FORWARD_PORT}"
pkill -f "kubectl port-forward.*--namespace ${NAMESPACE} .*svc/${SERVER_RELEASE}" 2>/dev/null || true
sleep 1

kubectl port-forward \
  --namespace "$NAMESPACE" \
  --context "$KUBE_CONTEXT" \
  "svc/$SERVER_RELEASE" \
  "${FORWARD_PORT}:8080" &
PF_PID=$!

info "Waiting for server at $SERVER_URL ..."
SERVER_READY=0
for i in $(seq 1 30); do
  if curl -sf -o /dev/null \
      -H "Authorization: Bearer $ADMIN_KEY" \
      "$SERVER_URL/api/v1/clusters"; then
    SERVER_READY=1
    break
  fi
  info "attempt $i/30 — retrying in 2s"
  sleep 2
done
if [[ $SERVER_READY -eq 0 ]]; then
  kill "$PF_PID" 2>/dev/null || true
  die "server did not become reachable after 60s (check: kubectl logs -n $NAMESPACE -l app=$SERVER_RELEASE --context $KUBE_CONTEXT)"
fi

# ── 6. Register cluster + get agent token ─────────────────────────────────────
step "Registering cluster '$CLUSTER_NAME'"

CLUSTER_TMP=$(mktemp); TMPFILES+=("$CLUSTER_TMP")
CLUSTER_HTTP=$(curl -s -o "$CLUSTER_TMP" -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/clusters" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"${CLUSTER_NAME}\"}")
CLUSTER_BODY=$(cat "$CLUSTER_TMP")

if [[ "$CLUSTER_HTTP" == "201" ]]; then
  CLUSTER_ID=$(printf '%s' "$CLUSTER_BODY" | jq -r '.id')
  AGENT_TOKEN=$(printf '%s' "$CLUSTER_BODY" | jq -r '.agentToken')
elif [[ "$CLUSTER_HTTP" == "409" ]]; then
  info "Cluster already exists — looking it up and rotating token"
  LIST=$(curl_api "GET /clusters" \
    "$SERVER_URL/api/v1/clusters" \
    -H "Authorization: Bearer $ADMIN_KEY")
  CLUSTER_ID=$(printf '%s' "$LIST" | jq -r --arg n "$CLUSTER_NAME" '.items[] | select(.name==$n) | .id')
  [[ -n "$CLUSTER_ID" && "$CLUSTER_ID" != "null" ]] \
    || die "cluster '$CLUSTER_NAME' not found in list: $LIST"
  ROTATE=$(curl_api "POST /clusters/$CLUSTER_ID/rotate-token" \
    -X POST "$SERVER_URL/api/v1/clusters/$CLUSTER_ID/rotate-token" \
    -H "Authorization: Bearer $ADMIN_KEY")
  AGENT_TOKEN=$(printf '%s' "$ROTATE" | jq -r '.agentToken')
else
  die "POST /api/v1/clusters failed (HTTP $CLUSTER_HTTP): $CLUSTER_BODY"
fi

[[ -n "$CLUSTER_ID"  && "$CLUSTER_ID"  != "null" ]] || die "could not determine cluster ID"
[[ -n "$AGENT_TOKEN" && "$AGENT_TOKEN" != "null" ]] || die "could not determine agent token"
info "Cluster ID: $CLUSTER_ID"

# ── 7. Install pond-agent ─────────────────────────────────────────────────────
step "Installing pond-agent${CHART_VERSION:+ v${CHART_VERSION}} into namespace '$NAMESPACE'"

helm upgrade --install "$AGENT_RELEASE" \
  "${GHCR_CHARTS}/${AGENT_RELEASE}" \
  ${HELM_VERSION_FLAG[@]+"${HELM_VERSION_FLAG[@]}"} \
  --kube-context "$KUBE_CONTEXT" \
  --namespace "$NAMESPACE" \
  --set image.repository="${GHCR_IMAGES}/pond-agent" \
  --set image.tag="$IMAGE_TAG" \
  --set "serverAddr=${SERVER_RELEASE}.${NAMESPACE}.svc.cluster.local:8080" \
  --set agentToken="$AGENT_TOKEN" \
  --wait --timeout 2m \
  || die "pond-agent Helm install failed (logs: kubectl logs -n $NAMESPACE -l app=$AGENT_RELEASE --context $KUBE_CONTEXT)"

# ── 8. Mint CLI token ─────────────────────────────────────────────────────────
step "Minting CLI token"
TOKEN_BODY=$(curl_api "POST /tokens" \
  -X POST "$SERVER_URL/api/v1/tokens" \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"role\":\"admin\",\"description\":\"setup-${CONTEXT_NAME}\"}")
CLI_TOKEN=$(printf '%s' "$TOKEN_BODY" | jq -r '.token')
[[ -n "$CLI_TOKEN" && "$CLI_TOKEN" != "null" ]] || die "could not mint CLI token"

# ── 9. Create "default" project ───────────────────────────────────────────────
step "Creating project 'default'"

PROJ_TMP=$(mktemp); TMPFILES+=("$PROJ_TMP")
PROJ_HTTP=$(curl -s -o "$PROJ_TMP" -w "%{http_code}" \
  -X POST "$SERVER_URL/api/v1/projects" \
  -H "Authorization: Bearer $CLI_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"default"}')
PROJ_BODY=$(cat "$PROJ_TMP")

if [[ "$PROJ_HTTP" == "201" ]]; then
  PROJECT_ID=$(printf '%s' "$PROJ_BODY" | jq -r '.id')
elif [[ "$PROJ_HTTP" == "409" ]]; then
  info "Project 'default' already exists — looking it up"
  LIST=$(curl_api "GET /projects" \
    "$SERVER_URL/api/v1/projects" \
    -H "Authorization: Bearer $CLI_TOKEN")
  PROJECT_ID=$(printf '%s' "$LIST" | jq -r '.items[] | select(.name=="default") | .id')
else
  die "POST /api/v1/projects failed (HTTP $PROJ_HTTP): $PROJ_BODY"
fi

[[ -n "$PROJECT_ID" && "$PROJECT_ID" != "null" ]] || die "could not determine project ID"
info "Project ID: $PROJECT_ID"

# ── 10. Create environments ───────────────────────────────────────────────────
step "Creating environments"

DEFAULT_ENV=""
IFS=',' read -ra ENV_NAMES <<< "$ENVIRONMENTS"
for env_name in "${ENV_NAMES[@]}"; do
  env_name="${env_name// /}"
  [[ -n "$env_name" ]] || continue
  DEFAULT_ENV="${DEFAULT_ENV:-$env_name}"

  env_namespace="${NAMESPACE}-${env_name}"
  info "Creating environment '$env_name' (namespace: $env_namespace)"

  kubectl --context "$KUBE_CONTEXT" create namespace "$env_namespace" \
    --dry-run=client -o yaml \
    | kubectl --context "$KUBE_CONTEXT" apply -f -

  ENV_TMP=$(mktemp); TMPFILES+=("$ENV_TMP")
  ENV_HTTP=$(curl -s -o "$ENV_TMP" -w "%{http_code}" \
    -X POST "$SERVER_URL/api/v1/projects/$PROJECT_ID/environments" \
    -H "Authorization: Bearer $CLI_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"${env_name}\",\"namespace\":\"${env_namespace}\",\"clusterId\":\"${CLUSTER_ID}\"}")
  ENV_BODY=$(cat "$ENV_TMP")

  if [[ "$ENV_HTTP" == "201" ]]; then
    info "  ✓ environment '$env_name' created"
  elif [[ "$ENV_HTTP" == "409" ]]; then
    info "  - environment '$env_name' already exists"
  else
    die "POST /api/v1/projects/$PROJECT_ID/environments failed (HTTP $ENV_HTTP): $ENV_BODY"
  fi
done

# ── 11. Configure pond CLI context ────────────────────────────────────────────
step "Configuring pond CLI context '$CONTEXT_NAME'"

if [[ -n "$DEFAULT_ENV" ]]; then
  info "Default environment for context: '$DEFAULT_ENV' (first of: ${ENVIRONMENTS})"
  info "  Override per command with --env <name> or POND_ENV"
else
  info "No environments created — context will have no default environment"
fi

"$POND_CLI" context add "$CONTEXT_NAME" \
  --server "$SERVER_URL" \
  --token "$CLI_TOKEN" \
  --project "$PROJECT_ID" \
  --env "$DEFAULT_ENV" \
  --kube-context "$KUBE_CONTEXT" \
  --kube-namespace "$NAMESPACE" \
  --kube-service "$SERVER_RELEASE" \
  --kubectl-forward-port "$FORWARD_PORT"

"$POND_CLI" context use "$CONTEXT_NAME"
info "Active context set to '$CONTEXT_NAME'"

# ── 12. Save state file ───────────────────────────────────────────────────────
STATE_DIR="${HOME}/.pond"
mkdir -p "$STATE_DIR"
STATE_FILE="${STATE_DIR}/setup-${CONTEXT_NAME}.env"
printf '%s\n' \
  "POND_SERVER_URL=${SERVER_URL}" \
  "POND_ADMIN_KEY=${ADMIN_KEY}" \
  "POND_JWT_SECRET=${JWT_SECRET}" \
  "POND_CLUSTER_ID=${CLUSTER_ID}" \
  "POND_PROJECT_ID=${PROJECT_ID}" \
  "POND_PF_PID=${PF_PID}" \
  "POND_KUBECTL_FORWARD_PORT=${FORWARD_PORT}" \
  "POND_KUBE_CONTEXT=${KUBE_CONTEXT}" \
  "POND_KUBE_NAMESPACE=${NAMESPACE}" \
  "POND_KUBE_SERVICE=${SERVER_RELEASE}" \
  > "$STATE_FILE"
chmod 600 "$STATE_FILE"

# ── Summary ───────────────────────────────────────────────────────────────────
echo
echo "${bold}══════════════════════════════════════════════════════${reset}"
echo "${bold}  Pond Setup Complete${reset}"
echo "${bold}══════════════════════════════════════════════════════${reset}"
echo
echo "  Kubernetes context:  ${KUBE_CONTEXT}"
echo "  Namespace:           ${NAMESPACE}"
echo "  Charts:              ${GHCR_CHARTS} (${CHART_VERSION:-latest})"
echo "  Images:              ${GHCR_IMAGES}/pond-{server,agent}:${IMAGE_TAG}"
echo "  Cluster:             ${CLUSTER_NAME} (${CLUSTER_ID})"
echo
echo "  Project:             default (${PROJECT_ID})"
IFS=',' read -ra _ENVS <<< "$ENVIRONMENTS"
_ENV_LIST=""
for _e in "${_ENVS[@]}"; do
  _e="${_e// /}"
  [[ -n "$_e" ]] && _ENV_LIST="${_ENV_LIST:+${_ENV_LIST}, }${_e}"
done
echo "  Environments:        ${_ENV_LIST}"
echo
echo "  Server URL:          ${SERVER_URL}"
echo "  Port-forward:        localhost:${FORWARD_PORT} -> svc/${SERVER_RELEASE}:8080 (PID ${PF_PID})"
echo "  Pond CLI context:    ${CONTEXT_NAME}  ${bold}[active]${reset}"
echo "  Default environment: ${DEFAULT_ENV:-<none>}"
echo
echo "  State file:          ${STATE_FILE}"
echo "  (contains ADMIN_KEY and JWT_SECRET — keep secure)"
echo
echo "${bold}To restart the port-forward after the session ends:${reset}"
echo "  kubectl port-forward --namespace ${NAMESPACE} --context ${KUBE_CONTEXT} \\"
echo "    svc/pond-server ${FORWARD_PORT}:8080 &"
echo
echo "${bold}To remove this installation:${reset}"
echo "  helm uninstall ${SERVER_RELEASE} ${AGENT_RELEASE} -n ${NAMESPACE} --kube-context ${KUBE_CONTEXT}"
echo "  kubectl delete namespace ${NAMESPACE} --context ${KUBE_CONTEXT}"
echo "  pond context remove ${CONTEXT_NAME}"
echo "${bold}══════════════════════════════════════════════════════${reset}"

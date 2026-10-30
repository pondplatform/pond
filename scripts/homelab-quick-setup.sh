#!/usr/bin/env bash
# Sets up pond on a Kubernetes cluster you control (a homelab, k3s, a spare VM, ...):
# pond-server + pond-agent, a "default" project with environments, and a pond CLI
# context. It asks a few questions, shows the plan, and changes nothing until you confirm.
#
#   curl -fsSL https://raw.githubusercontent.com/pondplatform/pond/main/scripts/homelab-quick-setup.sh | bash
#
# Every question can be answered with a flag instead; see --help.
# Needs: kubectl, helm, curl, jq, openssl, and cluster-admin on the target cluster.

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
KUBE_CONTEXT=""
NAMESPACE="pond"
CONTEXT_NAME=""
CLUSTER_NAME=""
IMAGE_TAG="v0.3.0-alpha3"
CHART_VERSION=""        # empty = image tag without "v"
FORWARD_PORT=""         # empty = random free local port
ENVIRONMENTS=""
FEATURES=""             # full | none | postgres,gateway
DOMAIN=""
HOSTNAMES=""            # env | primary
TLS=""                  # cloudflare | self-signed | issuer:<name>

SERVER_RELEASE="pond-server"
AGENT_RELEASE="pond-agent"
GHCR_CHARTS="oci://ghcr.io/pondplatform/charts"
GHCR_IMAGES="ghcr.io/pondplatform"
ENVOY_GATEWAY_VERSION="v1.9.2"
CERT_MANAGER_VERSION="v1.21.2"

# ── Output ────────────────────────────────────────────────────────────────────
bold=$'\033[1m'; dim=$'\033[2m'; reset=$'\033[0m'
green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'; cyan=$'\033[36m'
step()    { echo; echo "${bold}${green}▶ $*${reset}"; }
info()    { echo "  ${yellow}$*${reset}"; }
ok()      { echo "  ${green}✓${reset} $*"; }
note()    { echo "  • $*"; }
warn()    { echo "  ${bold}${yellow}⚠ $*${reset}" >&2; }
error()   { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; }
die()     { error "$*"; exit 1; }
explain() { local l; for l in "$@"; do echo "  ${dim}${l}${reset}"; done; }
section() { echo; echo "${bold}${cyan}[$1/4] $2${reset}"; }

usage() {
  cat <<EOF
Usage: homelab-quick-setup.sh [OPTIONS]

Sets up pond on a Kubernetes cluster. Anything not given as a flag is asked for;
the plan is shown and nothing changes until you confirm.

Options:
  --kube-context <ctx>      kubectl context of the target cluster
  --environments <list>     comma-separated environment names, e.g. production,staging
  --features <list>         optional parts: full (all), none, or a list of
                              postgres  managed Postgres via CloudNativePG
                              gateway   HTTPS for exposed services
  --domain <domain>         gateway: domain that service hostnames live under
  --hostnames <style>       gateway: primary  first environment at <service>.<domain> (default)
                                     env      <service>.<env>.<domain>
  --tls <source>            gateway: where certificates come from
                              cloudflare     Let's Encrypt via Cloudflare DNS
                              self-signed    private CA created in the cluster
                              issuer:<name>  an existing cert-manager ClusterIssuer
  -h, --help                show this help

Advanced:
  --namespace <ns>            namespace for pond itself          (default: pond)
  --context-name <name>       pond CLI context name              (default: <kube-context>)
  --cluster-name <name>       name pond registers the cluster as (default: <kube-context>)
  --image-tag <tag>           pond image tag                     (default: ${IMAGE_TAG})
  --chart-version <ver>       pond Helm chart version            (default: tag without "v")
  --kubectl-forward-port <p>  local port the CLI forwards to     (default: random)

Environment:
  CLOUDFLARE_API_TOKEN        --tls cloudflare: token with Zone:DNS:Edit and Zone:Zone:Read
EOF
}

parse_args() {
  local flag value
  while [[ $# -gt 0 ]]; do
    flag="$1" value="${2-}"
    case "$flag" in
      -h|--help)              usage; exit 0 ;;
      --kube-context)         KUBE_CONTEXT="$value" ;;
      --environments)         ENVIRONMENTS="$value" ;;
      --features)             FEATURES="$value" ;;
      --domain)               DOMAIN="$value" ;;
      --hostnames)            HOSTNAMES="$value" ;;
      --tls)                  TLS="$value" ;;
      --namespace)            NAMESPACE="$value" ;;
      --context-name)         CONTEXT_NAME="$value" ;;
      --cluster-name)         CLUSTER_NAME="$value" ;;
      --image-tag)            IMAGE_TAG="$value" ;;
      --chart-version)        CHART_VERSION="$value" ;;
      --kubectl-forward-port) FORWARD_PORT="$value" ;;
      *) die "Unknown argument: $flag (run with --help for usage)" ;;
    esac
    [[ $# -ge 2 ]] || die "$flag needs a value (run with --help for usage)"
    shift 2
  done
}

# ── Prompts (read from /dev/tty so `curl | bash` works) ───────────────────────

# prompt_select <prompt> <default> <option>... — prints the chosen option
prompt_select() {
  local prompt="$1" default="$2"; shift 2
  local options=("$@") i choice default_idx=1
  echo "${bold}${prompt}${reset}" >/dev/tty
  for i in "${!options[@]}"; do
    if [[ "${options[$i]}" == "$default" ]]; then
      default_idx=$(( i + 1 ))
      printf '  %d) %s %s\n' "$(( i + 1 ))" "${options[$i]}" "${bold}(default)${reset}" >/dev/tty
    else
      printf '  %d) %s\n' "$(( i + 1 ))" "${options[$i]}" >/dev/tty
    fi
  done
  while true; do
    printf 'Select [%d]: ' "$default_idx" >/dev/tty
    read -r choice </dev/tty
    choice="${choice:-$default_idx}"
    if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#options[@]} )); then
      printf '%s' "${options[$(( choice - 1 ))]}"
      return 0
    fi
    echo "  Please enter a number between 1 and ${#options[@]}." >/dev/tty
  done
}

# prompt_input <prompt> <default> — prints the entered value, or the default
prompt_input() {
  local value
  if [[ -n "$2" ]]; then
    printf '%s [%s]: ' "${bold}$1${reset}" "$2" >/dev/tty
  else
    printf '%s: ' "${bold}$1${reset}" >/dev/tty
  fi
  read -r value </dev/tty
  printf '%s' "${value:-$2}"
}

# prompt_secret <prompt> — prints the entered value without echoing it
prompt_secret() {
  local value
  printf '%s: ' "${bold}$1${reset}" >/dev/tty
  read -rs value </dev/tty
  echo >/dev/tty
  printf '%s' "$value"
}

# prompt_yesno <prompt> <default y|n> — succeeds on yes
prompt_yesno() {
  local hint="y/N" ans
  [[ "$2" == "y" ]] && hint="Y/n"
  while true; do
    printf '%s [%s]: ' "${bold}$1${reset}" "$hint" >/dev/tty
    read -r ans </dev/tty
    case "$(printf '%s' "${ans:-$2}" | tr '[:upper:]' '[:lower:]')" in
      y|yes) return 0 ;;
      n|no)  return 1 ;;
    esac
    echo "  Please answer y or n." >/dev/tty
  done
}

# ── Helpers ───────────────────────────────────────────────────────────────────
kc()   { kubectl --context "$KUBE_CONTEXT" "$@"; }
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

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

# env_base_host <env> — the host the environment's services live under
env_base_host() {
  if [[ "$HOSTNAMES" == "primary" && "$1" == "${ENV_LIST[0]}" ]]; then
    printf '%s' "$DOMAIN"
  else
    printf '%s' "$1.$DOMAIN"
  fi
}

crd_exists() { kc get crd "$1" &>/dev/null; }

TMPFILES=()
PF_PID=""
cleanup() {
  local f
  for f in ${TMPFILES[@]+"${TMPFILES[@]}"}; do rm -f "$f" 2>/dev/null || true; done
  # The CLI starts its own port-forward when needed; this one only served the setup.
  if [[ -n "$PF_PID" ]]; then kill "$PF_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT

# ── Preflight: this machine ───────────────────────────────────────────────────
preflight_local() {
  local missing=() cmd
  for cmd in kubectl helm curl jq openssl; do
    command -v "$cmd" &>/dev/null || missing+=("$cmd")
  done
  [[ ${#missing[@]} -eq 0 ]] || die "missing tools: ${missing[*]} — install them and re-run"
  (: </dev/tty) 2>/dev/null \
    || die "this script needs an interactive terminal: it asks for confirmation before changing anything"

  POND_CLI=""
  BUILD_CLI="false"
  if command -v pond &>/dev/null; then
    POND_CLI="pond"
  elif [[ -x ./build/pond ]]; then
    POND_CLI="./build/pond"
  elif [[ -f ./cli/main.go ]] && command -v go &>/dev/null; then
    POND_CLI="./build/pond"
    BUILD_CLI="true"
  else
    echo
    warn "The pond CLI isn't installed."
    explain "Pond installs fine without it, but you need the CLI to deploy services." \
            "Prebuilt binaries aren't published yet; build it from a checkout of the repo" \
            "with 'make build-cli'. At the end this script prints the command that points" \
            "the CLI at your new installation."
    prompt_yesno "Continue without the CLI?" y || exit 0
  fi
}

# ── [1/4] Cluster ─────────────────────────────────────────────────────────────
ask_cluster() {
  section 1 "Cluster"
  if [[ -n "$KUBE_CONTEXT" ]]; then
    note "kubectl context: ${KUBE_CONTEXT} (from --kube-context)"
  else
    explain "Pond's server, agent and anything you deploy run in this cluster." \
            "Double-check it isn't a shared or production cluster."
    local contexts=() ctx current
    while IFS= read -r ctx; do contexts+=("$ctx"); done \
      < <(kubectl config get-contexts -o name 2>/dev/null | sort)
    [[ ${#contexts[@]} -gt 0 ]] || die "no kubectl contexts found — configure kubeconfig first"
    current=$(kubectl config current-context 2>/dev/null || true)
    KUBE_CONTEXT=$(prompt_select "Which kubectl context?" "${current:-${contexts[0]}}" "${contexts[@]}")
  fi
  preflight_cluster
}

preflight_cluster() {
  kubectl config get-contexts "$KUBE_CONTEXT" --no-headers &>/dev/null \
    || die "kubectl context '$KUBE_CONTEXT' not found — check: kubectl config get-contexts"

  API_SERVER=$(kubectl config view --minify --context "$KUBE_CONTEXT" -o jsonpath='{.clusters[0].cluster.server}')
  K8S_VERSION=$( (kc version -o json --request-timeout=10s 2>/dev/null || true) | jq -r '.serverVersion.gitVersion // empty')
  [[ -n "$K8S_VERSION" ]] \
    || die "cannot reach the cluster at ${API_SERVER} — check: kubectl --context $KUBE_CONTEXT get nodes"
  NODE_COUNT=$( (kc get nodes --no-headers 2>/dev/null || true) | wc -l | tr -d ' ')
  local nodes="${NODE_COUNT} node"; [[ "$NODE_COUNT" == "1" ]] || nodes="${nodes}s"
  ok "reachable: ${API_SERVER} (Kubernetes ${K8S_VERSION}, ${nodes})"

  kc auth can-i '*' '*' --all-namespaces &>/dev/null \
    || die "your access to this cluster isn't cluster-admin — pond installs cluster-wide roles and CRDs, so it needs it"
  ok "cluster-admin access"

  DEFAULT_SC=$(kc get storageclass -o json | jq -r '[.items[] | select(
      .metadata.annotations["storageclass.kubernetes.io/is-default-class"] == "true" or
      .metadata.annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true")
    | .metadata.name] | first // empty')
  if [[ -z "$DEFAULT_SC" ]]; then
    error "the cluster has no default StorageClass"
    explain "The pond agent keeps OpenTofu state on a volume, and managed Postgres databases" \
            "need volumes too, so the install would hang waiting for storage." \
            "Install a provisioner (local-path-provisioner is the usual homelab choice), mark" \
            "it as default, and re-run."
    exit 1
  fi
  ok "default StorageClass: ${DEFAULT_SC}"

  EXISTING_INSTALL="false"
  JWT_SECRET=""
  ADMIN_KEY=""
  if helm status "$SERVER_RELEASE" --namespace "$NAMESPACE" --kube-context "$KUBE_CONTEXT" &>/dev/null; then
    EXISTING_INSTALL="true"
    local values
    values=$(helm get values "$SERVER_RELEASE" --namespace "$NAMESPACE" --kube-context "$KUBE_CONTEXT" -o json)
    JWT_SECRET=$(jq -r '.jwtSecret // empty' <<<"$values")
    ADMIN_KEY=$(jq -r '.adminKey // empty' <<<"$values")
    note "pond is already installed in namespace '${NAMESPACE}': it will be upgraded, existing tokens keep working"
  fi

  detect_gateway_controller
  CNPG_PRESENT="false";         crd_exists clusters.postgresql.cnpg.io && CNPG_PRESENT="true"
  CERT_MANAGER_PRESENT="false"; crd_exists clusterissuers.cert-manager.io && CERT_MANAGER_PRESENT="true"
  return 0
}

detect_gateway_controller() {
  TRAEFIK_STATE=""
  ENVOY_PRESENT="false"
  if ! kc get helmcharts.helm.cattle.io traefik --namespace kube-system &>/dev/null; then
    GATEWAY_CONTROLLER="envoy"
    GATEWAY_CLASS="envoy-gateway"
    HTTP_PORT=80
    HTTPS_PORT=443
    crd_exists envoyproxies.gateway.envoyproxy.io && ENVOY_PRESENT="true"
    return 0
  fi

  GATEWAY_CONTROLLER="traefik"
  local deploy major
  deploy=$(kc get deploy traefik --namespace kube-system -o json 2>/dev/null || true)
  if [[ -z "$deploy" ]]; then
    TRAEFIK_STATE="not-running"
    return 0
  fi
  TRAEFIK_VERSION=$(jq -r '.spec.template.spec.containers[] | select(.name=="traefik") | .image | split(":")[-1]' <<<"$deploy")
  # traefik binds a listener to the entryPoint serving that port, so listeners use
  # the container ports, not the 80/443 its Service publishes.
  HTTP_PORT=$(jq -r '[.spec.template.spec.containers[] | select(.name=="traefik") | .ports[]? | select(.name=="web") | .containerPort] | first // 8000' <<<"$deploy")
  HTTPS_PORT=$(jq -r '[.spec.template.spec.containers[] | select(.name=="traefik") | .ports[]? | select(.name=="websecure") | .containerPort] | first // 8443' <<<"$deploy")

  major="${TRAEFIK_VERSION#v}"; major="${major%%.*}"
  if [[ "$major" == "2" || "$major" == "1" ]]; then
    TRAEFIK_STATE="too-old"
  elif ! kc get crd httproutes.gateway.networking.k8s.io -o json 2>/dev/null \
         | jq -e '.spec.versions[] | select(.name=="v1" and .served)' >/dev/null; then
    TRAEFIK_STATE="no-crds"
  elif jq -e '[.spec.template.spec.containers[] | select(.name=="traefik") | .args[]?]
              | any(startswith("--providers.kubernetesgateway"))' <<<"$deploy" >/dev/null; then
    TRAEFIK_STATE="enabled"
    GATEWAY_CLASS=$(traefik_gateway_class)
    GATEWAY_CLASS="${GATEWAY_CLASS:-traefik}"
  elif kc get helmchartconfigs.helm.cattle.io traefik --namespace kube-system &>/dev/null; then
    TRAEFIK_STATE="custom-config"
  else
    TRAEFIK_STATE="needs-config"
    GATEWAY_CLASS="traefik"
  fi
}

traefik_gateway_class() {
  { kc get gatewayclass -o json 2>/dev/null || echo '{"items":[]}'; } \
    | jq -r '[.items[] | select(.spec.controllerName=="traefik.io/gateway-controller") | .metadata.name] | first // empty'
}

# ── [2/4] Environments ────────────────────────────────────────────────────────
ask_environments() {
  section 2 "Environments"
  local from_flag="false" env invalid
  [[ -n "$ENVIRONMENTS" ]] && from_flag="true"
  if [[ "$from_flag" == "false" ]]; then
    explain "An environment is where services get deployed, e.g. 'production' or 'staging'." \
            "Each one is its own namespace (${NAMESPACE}-<env>). Most people start with one."
  fi
  while true; do
    if [[ "$from_flag" == "false" ]]; then
      ENVIRONMENTS=$(prompt_input "Environments (comma-separated)" "production")
    fi
    ENV_LIST=()
    invalid=""
    IFS=',' read -ra _envs <<< "$ENVIRONMENTS"
    for env in ${_envs[@]+"${_envs[@]}"}; do
      env=$(lower "${env// /}")
      [[ -n "$env" ]] || continue
      if [[ ! "$env" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || (( ${#NAMESPACE} + 1 + ${#env} > 63 )); then
        invalid="$env"
        break
      fi
      ENV_LIST+=("$env")
    done
    if [[ -z "$invalid" && ${#ENV_LIST[@]} -gt 0 ]]; then
      break
    fi
    local msg="'${invalid}' can't be an environment name: use lowercase letters, digits and '-'"
    [[ -n "$invalid" ]] || msg="give at least one environment name"
    [[ "$from_flag" == "true" ]] && die "--environments: $msg"
    warn "$msg"
  done
  if [[ "$from_flag" == "true" ]]; then
    note "environments: ${ENV_LIST[*]} (from --environments)"
  fi
}

# ── [3/4] + [4/4] Optional features ───────────────────────────────────────────
parse_features_flag() {
  INSTALL_POSTGRES="false"
  INSTALL_GATEWAY="false"
  local f
  IFS=',' read -ra _features <<< "$FEATURES"
  for f in ${_features[@]+"${_features[@]}"}; do
    case "${f// /}" in
      full)     INSTALL_POSTGRES="true"; INSTALL_GATEWAY="true" ;;
      postgres) INSTALL_POSTGRES="true" ;;
      gateway)  INSTALL_GATEWAY="true" ;;
      none|"")  ;;
      *) die "--features: unknown feature '${f// /}' (valid: full, none, postgres, gateway)" ;;
    esac
  done
}

ask_postgres() {
  section 3 "Managed Postgres"
  if [[ -n "$FEATURES" ]]; then
    note "managed Postgres: $([[ "$INSTALL_POSTGRES" == "true" ]] && echo yes || echo no) (from --features)"
    return 0
  fi
  explain "Lets pond create a Postgres database for each service that declares a" \
          "'postgres' dependency. Without it, services must use a database you run" \
          "yourself ('managed: false' in their deploy input)."
  if [[ "$CNPG_PRESENT" == "true" ]]; then
    explain "CloudNativePG is already installed in this cluster and will be used as is."
  else
    explain "Installs the CloudNativePG operator, cluster-wide (namespace cnpg-system)."
  fi
  INSTALL_POSTGRES="false"
  if prompt_yesno "Set up managed Postgres?" y; then INSTALL_POSTGRES="true"; fi
}

# https_blocker — prints why HTTPS can't be set up on this cluster (empty = it can)
https_blocker() {
  case "$TRAEFIK_STATE" in
    not-running)
      echo "k3s's bundled traefik isn't running yet. Wait until it is"
      echo "(kubectl -n kube-system get pods) and re-run." ;;
    too-old)
      echo "k3s's bundled traefik is ${TRAEFIK_VERSION}, which has no Gateway API support."
      echo "Upgrade k3s to 1.32.2 or newer, or disable the bundled traefik"
      echo "(k3s --disable=traefik) and re-run to use Envoy Gateway instead." ;;
    no-crds)
      echo "The cluster lacks the Gateway API v1 CRDs that k3s's traefik needs."
      echo "Upgrade k3s to 1.32.2 or newer, or disable the bundled traefik and re-run." ;;
    custom-config)
      echo "kube-system/traefik already has a HelmChartConfig with your own settings, and"
      echo "pond won't overwrite it. Add this to its valuesContent, wait for traefik to"
      echo "restart, then re-run:"
      echo "    providers:"
      echo "      kubernetesGateway:"
      echo "        enabled: true"
      echo "    gateway:"
      echo "      enabled: false" ;;
  esac
}

ask_https() {
  section 4 "HTTPS for exposed services"
  local blocker line
  blocker=$(https_blocker)

  if [[ -n "$FEATURES" ]]; then
    if [[ "$INSTALL_GATEWAY" == "true" && -n "$blocker" ]]; then
      error "--features gateway can't be set up on this cluster:"
      while IFS= read -r line; do explain "$line"; done <<<"$blocker"
      exit 1
    fi
    note "HTTPS: $([[ "$INSTALL_GATEWAY" == "true" ]] && echo yes || echo no) (from --features)"
  else
    explain "Gives services that set gateway.http.enabled in pond.yml an HTTPS URL under a" \
            "domain you control. Without it, those services aren't reachable from outside."
    if [[ -n "$blocker" ]]; then
      echo
      warn "HTTPS can't be set up on this cluster yet:"
      while IFS= read -r line; do explain "$line"; done <<<"$blocker"
      INSTALL_GATEWAY="false"
      prompt_yesno "Continue without HTTPS?" y || exit 0
      return 0
    fi
    explain_https_components
    INSTALL_GATEWAY="false"
    if prompt_yesno "Set up HTTPS?" y; then INSTALL_GATEWAY="true"; fi
  fi

  if [[ "$INSTALL_GATEWAY" != "true" ]]; then
    TLS=""
    return 0
  fi
  ask_domain
  ask_hostnames
  ask_tls
}

explain_https_components() {
  local gateway certs="cert-manager, installed into namespace cert-manager"
  [[ "$CERT_MANAGER_PRESENT" == "true" ]] && certs="the cert-manager already in the cluster"
  if [[ "$GATEWAY_CONTROLLER" == "traefik" ]]; then
    gateway="k3s's bundled traefik, with its Gateway API support turned on (restarts once)"
    [[ "$TRAEFIK_STATE" == "enabled" ]] && gateway="k3s's bundled traefik, which already serves Gateway API"
  else
    gateway="Envoy Gateway, installed into namespace envoy-gateway-system"
    [[ "$ENVOY_PRESENT" == "true" ]] && gateway="the Envoy Gateway already in the cluster"
  fi
  explain "Uses, cluster-wide:" \
          "  gateway       ${gateway}" \
          "  certificates  ${certs}"
  if [[ "$GATEWAY_CONTROLLER" == "envoy" ]]; then
    explain "Envoy needs a LoadBalancer address with ports 80/443 free (MetalLB," \
            "kube-vip, k3s's ServiceLB, or a cloud load balancer)."
  fi
  return 0
}

ask_domain() {
  local from_flag="false"
  [[ -n "$DOMAIN" ]] && from_flag="true"
  [[ "$from_flag" == "true" ]] || explain "" "The domain whose DNS you manage; services get hostnames under it."
  while true; do
    [[ "$from_flag" == "true" ]] || DOMAIN=$(prompt_input "Domain (e.g. home.example.com)" "")
    DOMAIN=$(lower "$DOMAIN")
    if [[ "$DOMAIN" =~ ^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z][-a-z0-9]*[a-z0-9]$ ]]; then
      break
    fi
    [[ "$from_flag" == "true" ]] && die "--domain: '$DOMAIN' isn't a domain name (e.g. home.example.com)"
    warn "'$DOMAIN' isn't a domain name"
  done
  [[ "$from_flag" == "false" ]] || note "domain: ${DOMAIN} (from --domain)"
}

ask_hostnames() {
  if [[ -n "$HOSTNAMES" ]]; then
    case "$HOSTNAMES" in
      env|primary) note "hostnames: ${HOSTNAMES} (from --hostnames)"; return 0 ;;
      *) die "--hostnames: unknown style '$HOSTNAMES' (valid: env, primary)" ;;
    esac
  fi
  local first="${ENV_LIST[0]}" second="${ENV_LIST[1]:-}" choice
  local env_example="todo.${first}.${DOMAIN}${second:+, todo.${second}.${DOMAIN}}"
  local primary_example="todo.${DOMAIN}${second:+, todo.${second}.${DOMAIN}}"
  explain "" "How service URLs look, for a service named 'todo':"
  choice=$(prompt_select "Hostname style" "primary   ${primary_example}" \
    "primary   ${primary_example}" \
    "env       ${env_example}")
  HOSTNAMES="${choice%% *}"
}

ask_tls() {
  if [[ -n "$TLS" ]]; then
    case "$TLS" in
      cloudflare)  CLUSTER_ISSUER="letsencrypt" ;;
      self-signed) CLUSTER_ISSUER="pond-ca" ;;
      issuer:?*)   CLUSTER_ISSUER="${TLS#issuer:}"; TLS="issuer"
                   kc get clusterissuer "$CLUSTER_ISSUER" &>/dev/null \
                     || die "--tls: ClusterIssuer '$CLUSTER_ISSUER' not found (check: kubectl get clusterissuers --context $KUBE_CONTEXT)" ;;
      issuer|issuer:) die "--tls issuer needs a ClusterIssuer name (--tls issuer:<name>)" ;;
      *) die "--tls: unknown source '$TLS' (valid: cloudflare, self-signed, issuer:<name>)" ;;
    esac
    note "certificates: ${TLS} (from --tls)"
  else
    explain "" "Each environment gets one wildcard certificate (*.<its host>), so new services" \
            "have HTTPS right away."
    local options=(
      "cloudflare    Let's Encrypt via Cloudflare DNS: trusted everywhere, needs an API token"
      "self-signed   private CA in the cluster: no account, but each device must trust the CA"
      "issuer        your own cert-manager ClusterIssuer: any DNS provider or CA"
    )
    while true; do
      TLS=$(prompt_select "Where should certificates come from?" "${options[0]}" "${options[@]}")
      TLS="${TLS%% *}"
      case "$TLS" in
        cloudflare)  CLUSTER_ISSUER="letsencrypt"; break ;;
        self-signed) CLUSTER_ISSUER="pond-ca"; break ;;
        issuer)
          local issuers=() name
          while IFS= read -r name; do [[ -n "$name" ]] && issuers+=("$name"); done \
            < <(kc get clusterissuers -o name 2>/dev/null | sed 's|.*/||')
          if [[ ${#issuers[@]} -eq 0 ]]; then
            warn "this cluster has no ClusterIssuer (is cert-manager installed?) — pick another option"
            continue
          fi
          CLUSTER_ISSUER=$(prompt_select "Which ClusterIssuer?" "${issuers[0]}" "${issuers[@]}")
          break ;;
      esac
    done
  fi

  if [[ "$TLS" == "cloudflare" && -z "${CLOUDFLARE_API_TOKEN:-}" ]]; then
    explain "" "Create an API token in the Cloudflare dashboard with Zone → DNS → Edit and" \
            "Zone → Zone → Read for ${DOMAIN}'s zone. It's stored as a Secret in the" \
            "cert-manager namespace and only used to prove you own the domain."
    while [[ -z "${CLOUDFLARE_API_TOKEN:-}" ]]; do
      CLOUDFLARE_API_TOKEN=$(prompt_secret "Cloudflare API token")
    done
  fi
}

# ── Review ────────────────────────────────────────────────────────────────────
tls_description() {
  case "$TLS" in
    cloudflare)  echo "Let's Encrypt via Cloudflare DNS" ;;
    self-signed) echo "private CA 'pond-ca' created in the cluster" ;;
    issuer)      echo "your ClusterIssuer '${CLUSTER_ISSUER}'" ;;
  esac
}

review() {
  local env nodes="${NODE_COUNT} node"
  [[ "$NODE_COUNT" == "1" ]] || nodes="${nodes}s"

  step "Review"
  echo "  ${bold}Target${reset}"
  echo "    kubectl context     ${KUBE_CONTEXT}"
  echo "    API server          ${API_SERVER} (${nodes})"
  echo
  echo "  ${bold}Pond${reset}"
  if [[ "$EXISTING_INSTALL" == "true" ]]; then
    echo "    server + agent      namespace ${NAMESPACE}, ${IMAGE_TAG} (upgrade of the existing install)"
  else
    echo "    server + agent      namespace ${NAMESPACE}, ${IMAGE_TAG}"
  fi
  echo "    project             default"
  for env in "${ENV_LIST[@]}"; do
    if [[ "$INSTALL_GATEWAY" == "true" ]]; then
      printf '    environment         %s → namespace %s-%s → https://<service>.%s\n' \
        "$env" "$NAMESPACE" "$env" "$(env_base_host "$env")"
    else
      printf '    environment         %s → namespace %s-%s\n' "$env" "$NAMESPACE" "$env"
    fi
  done
  echo

  echo "  ${bold}Cluster-wide${reset}"
  local any="false"
  if [[ "$INSTALL_POSTGRES" == "true" ]]; then
    any="true"
    if [[ "$CNPG_PRESENT" == "true" ]]; then
      echo "    CloudNativePG       already installed, used as is"
    else
      echo "    CloudNativePG       install into namespace cnpg-system"
    fi
  fi
  if [[ "$INSTALL_GATEWAY" == "true" ]]; then
    any="true"
    if [[ "$GATEWAY_CONTROLLER" == "traefik" ]]; then
      if [[ "$TRAEFIK_STATE" == "enabled" ]]; then
        echo "    gateway             k3s's traefik (already serves Gateway API)"
      else
        echo "    gateway             k3s's traefik: turn on Gateway API (traefik restarts once)"
      fi
    elif [[ "$ENVOY_PRESENT" == "true" ]]; then
      echo "    gateway             Envoy Gateway, already installed, used as is"
    else
      echo "    gateway             Envoy Gateway ${ENVOY_GATEWAY_VERSION}, install into envoy-gateway-system"
    fi
    if [[ "$CERT_MANAGER_PRESENT" == "true" ]]; then
      echo "    cert-manager        already installed, used as is"
    else
      echo "    cert-manager        ${CERT_MANAGER_VERSION}, install into namespace cert-manager"
    fi
    echo "    certificates        $(tls_description)"
  fi
  [[ "$any" == "true" ]] || echo "    nothing"
  echo

  echo "  ${bold}Good to know${reset}"
  note "Pond keeps its own data in a Postgres pod without a volume. If that pod restarts"
  echo "    (node reboot, upgrade), pond forgets projects, environments and tokens. Your"
  echo "    services keep running; re-run this script to set pond up again."
  if [[ "$INSTALL_GATEWAY" == "true" ]]; then
    note "You'll create DNS records pointing at the gateway; the end of the setup lists them."
    if [[ "$GATEWAY_CONTROLLER" == "envoy" ]]; then
      note "Envoy Gateway only gets an address if the cluster can hand out LoadBalancer IPs."
    fi
  fi
  if [[ "$TLS" == "self-signed" ]]; then
    note "Browsers warn about the certificates until a device trusts the CA (command shown at the end)."
  fi
  note "The admin key and JWT secret are saved to ~/.pond/setup-${CONTEXT_NAME}.env."
  echo

  if ! prompt_yesno "Proceed?" y; then
    echo "Nothing was changed."
    exit 0
  fi
}

# ── Install ───────────────────────────────────────────────────────────────────
build_cli() {
  [[ "$BUILD_CLI" == "true" ]] || return 0
  step "Building the pond CLI"
  go build -o ./build/pond ./cli/ || die "build failed — run: make build-cli"
}

install_server() {
  if [[ -z "$JWT_SECRET" || -z "$ADMIN_KEY" ]]; then
    [[ "$EXISTING_INSTALL" == "true" ]] \
      && warn "the existing install's secrets couldn't be read — new ones are generated, so earlier tokens stop working"
    JWT_SECRET=$(openssl rand -base64 32) || die "openssl failed"
    ADMIN_KEY=$(openssl rand -base64 24)  || die "openssl failed"
  fi

  step "Installing pond-server${CHART_VERSION:+ ${CHART_VERSION}} into namespace '$NAMESPACE'"
  # A values file keeps base64 characters in the secrets away from --set parsing.
  local vals_file
  vals_file=$(mktemp /tmp/pond-values.XXXXXX)
  TMPFILES+=("$vals_file")
  chmod 600 "$vals_file"
  printf 'jwtSecret: "%s"\nadminKey: "%s"\n' "$JWT_SECRET" "$ADMIN_KEY" > "$vals_file"

  helm upgrade --install "$SERVER_RELEASE" "${GHCR_CHARTS}/${SERVER_RELEASE}" \
    ${HELM_VERSION_FLAG[@]+"${HELM_VERSION_FLAG[@]}"} \
    --kube-context "$KUBE_CONTEXT" \
    --namespace "$NAMESPACE" \
    --create-namespace \
    --set image.repository="${GHCR_IMAGES}/pond-server" \
    --set image.tag="$IMAGE_TAG" \
    --values "$vals_file" \
    --wait --timeout 3m \
    || die "pond-server install failed (logs: kubectl logs -n $NAMESPACE -l app=$SERVER_RELEASE --context $KUBE_CONTEXT)"
}

install_postgres() {
  [[ "$INSTALL_POSTGRES" == "true" && "$CNPG_PRESENT" == "false" ]] || return 0
  step "Installing CloudNativePG (managed Postgres)"
  helm repo add cnpg https://cloudnative-pg.github.io/charts --force-update >/dev/null 2>&1 || true
  helm upgrade --install cnpg cnpg/cloudnative-pg \
    --kube-context "$KUBE_CONTEXT" \
    --namespace cnpg-system \
    --create-namespace \
    --wait --timeout 3m \
    || die "CloudNativePG install failed (logs: kubectl logs -n cnpg-system deploy/cnpg-cloudnative-pg --context $KUBE_CONTEXT)"
}

install_gateway_envoy() {
  if [[ "$ENVOY_PRESENT" == "false" ]]; then
    # Helm skips CRDs that already exist, so Gateway API CRDs shipped by the
    # distribution are left to their current owner.
    step "Installing Envoy Gateway ${ENVOY_GATEWAY_VERSION}"
    helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
      --version "$ENVOY_GATEWAY_VERSION" \
      --kube-context "$KUBE_CONTEXT" \
      --namespace envoy-gateway-system \
      --create-namespace \
      --wait --timeout 5m \
      || die "Envoy Gateway install failed (logs: kubectl logs -n envoy-gateway-system deploy/envoy-gateway --context $KUBE_CONTEXT)"
  fi

  # mergeGateways runs every environment's Gateway on one Envoy deployment, so the
  # cluster needs a single LoadBalancer address instead of one per environment.
  step "Creating GatewayClass '$GATEWAY_CLASS'"
  kc apply -f - <<EOF
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: pond
  namespace: envoy-gateway-system
spec:
  mergeGateways: true
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: $GATEWAY_CLASS
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
  parametersRef:
    group: gateway.envoyproxy.io
    kind: EnvoyProxy
    name: pond
    namespace: envoy-gateway-system
EOF
  kc wait --for=condition=Accepted "gatewayclass/$GATEWAY_CLASS" --timeout 2m \
    || die "GatewayClass '$GATEWAY_CLASS' was not accepted (check: kubectl describe gatewayclass/$GATEWAY_CLASS --context $KUBE_CONTEXT)"
}

install_gateway_traefik() {
  if [[ "$TRAEFIK_STATE" == "needs-config" ]]; then
    step "Turning on Gateway API support in k3s's traefik (traefik restarts once)"
    # gateway.enabled=false: each pond environment brings its own Gateway, so the
    # chart's default one isn't needed.
    kc apply -f - <<'EOF'
apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |-
    providers:
      kubernetesGateway:
        enabled: true
    gateway:
      enabled: false
EOF
    info "Waiting for k3s to redeploy traefik ..."
    GATEWAY_CLASS=""
    for _ in $(seq 1 90); do
      GATEWAY_CLASS=$(traefik_gateway_class)
      [[ -n "$GATEWAY_CLASS" ]] && break
      sleep 2
    done
    [[ -n "$GATEWAY_CLASS" ]] \
      || die "traefik registered no GatewayClass within 3 minutes (check: kubectl -n kube-system get pods,jobs --context $KUBE_CONTEXT)"
    kc rollout status deploy/traefik --namespace kube-system --timeout 3m \
      || die "traefik did not restart cleanly (check: kubectl -n kube-system get pods --context $KUBE_CONTEXT)"
  else
    step "Using k3s's traefik as the gateway (GatewayClass '$GATEWAY_CLASS')"
  fi
  kc wait --for=condition=Accepted "gatewayclass/$GATEWAY_CLASS" --timeout 2m \
    || die "GatewayClass '$GATEWAY_CLASS' was not accepted (check: kubectl describe gatewayclass/$GATEWAY_CLASS --context $KUBE_CONTEXT)"
}

install_tls() {
  if [[ "$CERT_MANAGER_PRESENT" == "false" ]]; then
    # Homelab resolvers often answer the domain locally (split DNS), which would hide
    # the challenge TXT record from cert-manager's self-check — ask public resolvers.
    step "Installing cert-manager ${CERT_MANAGER_VERSION}"
    helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
      --version "$CERT_MANAGER_VERSION" \
      --kube-context "$KUBE_CONTEXT" \
      --namespace cert-manager \
      --create-namespace \
      --set crds.enabled=true \
      --set-json 'dns01RecursiveNameservers="1.1.1.1:53,8.8.8.8:53"' \
      --set dns01RecursiveNameserversOnly=true \
      --wait --timeout 5m \
      || die "cert-manager install failed (logs: kubectl logs -n cert-manager deploy/cert-manager --context $KUBE_CONTEXT)"
  fi

  case "$TLS" in
    self-signed)
      # The CA outlives many leaf renewals on purpose: renewing it rotates its key,
      # and every device that trusts it would have to re-import it.
      step "Creating the private CA and ClusterIssuer '$CLUSTER_ISSUER'"
      kc apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: pond-selfsigned
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: pond-ca
  namespace: cert-manager
spec:
  isCA: true
  commonName: pond homelab CA
  secretName: pond-ca
  duration: 87600h
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    kind: ClusterIssuer
    name: pond-selfsigned
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: $CLUSTER_ISSUER
spec:
  ca:
    secretName: pond-ca
EOF
      ;;
    cloudflare)
      step "Creating Let's Encrypt ClusterIssuer '$CLUSTER_ISSUER' (DNS-01 via Cloudflare)"
      local token_file
      token_file=$(mktemp); TMPFILES+=("$token_file")
      chmod 600 "$token_file"
      printf '%s' "$CLOUDFLARE_API_TOKEN" > "$token_file"
      kc create secret generic cloudflare-api-token \
        --namespace cert-manager \
        --from-file=api-token="$token_file" \
        --dry-run=client -o yaml \
        | kc apply -f - \
        || die "could not store the Cloudflare API token"
      kc apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: $CLUSTER_ISSUER
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef:
      name: ${CLUSTER_ISSUER}-account-key
    solvers:
      - dns01:
          cloudflare:
            apiTokenSecretRef:
              name: cloudflare-api-token
              key: api-token
EOF
      ;;
    issuer)
      step "Using ClusterIssuer '$CLUSTER_ISSUER'" ;;
  esac
  kc wait --for=condition=Ready "clusterissuer/$CLUSTER_ISSUER" --timeout 2m \
    || die "ClusterIssuer '$CLUSTER_ISSUER' is not ready (check: kubectl describe clusterissuer/$CLUSTER_ISSUER --context $KUBE_CONTEXT)"
}

install_features() {
  install_postgres
  [[ "$INSTALL_GATEWAY" == "true" ]] || return 0
  if [[ "$GATEWAY_CONTROLLER" == "traefik" ]]; then
    install_gateway_traefik
  else
    install_gateway_envoy
  fi
  install_tls
}

start_port_forward() {
  step "Connecting to pond-server"
  pkill -f "kubectl port-forward.*--namespace ${NAMESPACE} .*svc/${SERVER_RELEASE}" 2>/dev/null || true
  sleep 1
  kubectl port-forward \
    --namespace "$NAMESPACE" \
    --context "$KUBE_CONTEXT" \
    "svc/$SERVER_RELEASE" \
    "${FORWARD_PORT}:8080" >/dev/null 2>&1 &
  PF_PID=$!

  local i
  for i in $(seq 1 30); do
    if curl -sf -o /dev/null -H "Authorization: Bearer $ADMIN_KEY" "$SERVER_URL/api/v1/clusters"; then
      return 0
    fi
    info "waiting for the server ($i/30)"
    sleep 2
  done
  die "server did not become reachable after 60s (check: kubectl logs -n $NAMESPACE -l app=$SERVER_RELEASE --context $KUBE_CONTEXT)"
}

register_cluster() {
  step "Registering the cluster as '$CLUSTER_NAME'"
  local tmp http body list rotate
  tmp=$(mktemp); TMPFILES+=("$tmp")
  http=$(curl -s -o "$tmp" -w "%{http_code}" \
    -X POST "$SERVER_URL/api/v1/clusters" \
    -H "Authorization: Bearer $ADMIN_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"${CLUSTER_NAME}\"}")
  body=$(cat "$tmp")

  if [[ "$http" == "201" ]]; then
    CLUSTER_ID=$(jq -r '.id' <<<"$body")
    AGENT_TOKEN=$(jq -r '.agentToken' <<<"$body")
  elif [[ "$http" == "409" ]]; then
    info "already registered — issuing a new agent token"
    list=$(curl_api "GET /clusters" "$SERVER_URL/api/v1/clusters" -H "Authorization: Bearer $ADMIN_KEY")
    CLUSTER_ID=$(jq -r --arg n "$CLUSTER_NAME" '.items[] | select(.name==$n) | .id' <<<"$list")
    [[ -n "$CLUSTER_ID" && "$CLUSTER_ID" != "null" ]] || die "cluster '$CLUSTER_NAME' not found in list: $list"
    rotate=$(curl_api "POST /clusters/$CLUSTER_ID/rotate-token" \
      -X POST "$SERVER_URL/api/v1/clusters/$CLUSTER_ID/rotate-token" \
      -H "Authorization: Bearer $ADMIN_KEY")
    AGENT_TOKEN=$(jq -r '.agentToken' <<<"$rotate")
  else
    die "POST /api/v1/clusters failed (HTTP $http): $body"
  fi
  [[ -n "$CLUSTER_ID"  && "$CLUSTER_ID"  != "null" ]] || die "could not determine the cluster ID"
  [[ -n "$AGENT_TOKEN" && "$AGENT_TOKEN" != "null" ]] || die "could not determine the agent token"
}

install_agent() {
  step "Installing pond-agent${CHART_VERSION:+ ${CHART_VERSION}} into namespace '$NAMESPACE'"
  helm upgrade --install "$AGENT_RELEASE" "${GHCR_CHARTS}/${AGENT_RELEASE}" \
    ${HELM_VERSION_FLAG[@]+"${HELM_VERSION_FLAG[@]}"} \
    --kube-context "$KUBE_CONTEXT" \
    --namespace "$NAMESPACE" \
    --set image.repository="${GHCR_IMAGES}/pond-agent" \
    --set image.tag="$IMAGE_TAG" \
    --set "serverAddr=${SERVER_RELEASE}.${NAMESPACE}.svc.cluster.local:8080" \
    --set agentToken="$AGENT_TOKEN" \
    --wait --timeout 2m \
    || die "pond-agent install failed (logs: kubectl logs -n $NAMESPACE -l app=$AGENT_RELEASE --context $KUBE_CONTEXT)"
}

create_project() {
  step "Creating project 'default'"
  local token_body tmp http body list
  token_body=$(curl_api "POST /tokens" \
    -X POST "$SERVER_URL/api/v1/tokens" \
    -H "Authorization: Bearer $ADMIN_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"role\":\"admin\",\"description\":\"setup-${CONTEXT_NAME}\"}")
  CLI_TOKEN=$(jq -r '.token' <<<"$token_body")
  [[ -n "$CLI_TOKEN" && "$CLI_TOKEN" != "null" ]] || die "could not mint a CLI token"

  tmp=$(mktemp); TMPFILES+=("$tmp")
  http=$(curl -s -o "$tmp" -w "%{http_code}" \
    -X POST "$SERVER_URL/api/v1/projects" \
    -H "Authorization: Bearer $CLI_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"default"}')
  body=$(cat "$tmp")
  if [[ "$http" == "201" ]]; then
    PROJECT_ID=$(jq -r '.id' <<<"$body")
  elif [[ "$http" == "409" ]]; then
    list=$(curl_api "GET /projects" "$SERVER_URL/api/v1/projects" -H "Authorization: Bearer $CLI_TOKEN")
    PROJECT_ID=$(jq -r '.items[] | select(.name=="default") | .id' <<<"$list")
  else
    die "POST /api/v1/projects failed (HTTP $http): $body"
  fi
  [[ -n "$PROJECT_ID" && "$PROJECT_ID" != "null" ]] || die "could not determine the project ID"
}

create_environments() {
  local env env_ns env_host tmp http body list env_id
  for env in "${ENV_LIST[@]}"; do
    env_ns="${NAMESPACE}-${env}"
    env_host=""
    [[ "$INSTALL_GATEWAY" == "true" ]] && env_host=$(env_base_host "$env")
    step "Creating environment '$env' (namespace $env_ns)"

    kc create namespace "$env_ns" --dry-run=client -o yaml | kc apply -f - >/dev/null

    tmp=$(mktemp); TMPFILES+=("$tmp")
    http=$(curl -s -o "$tmp" -w "%{http_code}" \
      -X POST "$SERVER_URL/api/v1/projects/$PROJECT_ID/environments" \
      -H "Authorization: Bearer $CLI_TOKEN" \
      -H "Content-Type: application/json" \
      -d "{\"name\":\"${env}\",\"namespace\":\"${env_ns}\",\"clusterId\":\"${CLUSTER_ID}\",\"defaultIngressBaseHost\":\"${env_host}\"}")
    body=$(cat "$tmp")
    if [[ "$http" == "409" ]]; then
      info "already exists"
      if [[ -n "$env_host" ]]; then
        list=$(curl_api "GET /projects/$PROJECT_ID/environments" \
          "$SERVER_URL/api/v1/projects/$PROJECT_ID/environments" \
          -H "Authorization: Bearer $CLI_TOKEN")
        env_id=$(jq -r --arg n "$env" '.items[] | select(.name==$n) | .id' <<<"$list")
        [[ -n "$env_id" && "$env_id" != "null" ]] || die "environment '$env' not found in list: $list"
        curl_api "PATCH /environments/$env_id" \
          -X PATCH "$SERVER_URL/api/v1/environments/$env_id" \
          -H "Authorization: Bearer $CLI_TOKEN" \
          -H "Content-Type: application/json" \
          -d "{\"defaultIngressBaseHost\":\"${env_host}\"}" >/dev/null
      fi
    elif [[ "$http" != "201" ]]; then
      die "POST /api/v1/projects/$PROJECT_ID/environments failed (HTTP $http): $body"
    fi

    [[ -n "$env_host" ]] || continue
    info "gateway for *.${env_host}"
    # Pond's HTTPRoutes attach to the listener named "http", so that name goes on
    # the TLS listener; plain HTTP gets its own listener that only redirects.
    kc apply -f - >/dev/null <<EOF || die "could not create the Gateway in namespace '$env_ns'"
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: gateway-tls
  namespace: $env_ns
spec:
  secretName: gateway-tls
  dnsNames:
    - "*.${env_host}"
  issuerRef:
    kind: ClusterIssuer
    name: $CLUSTER_ISSUER
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: gateway
  namespace: $env_ns
spec:
  gatewayClassName: $GATEWAY_CLASS
  listeners:
    - name: http
      protocol: HTTPS
      port: $HTTPS_PORT
      hostname: "*.${env_host}"
      tls:
        mode: Terminate
        certificateRefs:
          - name: gateway-tls
      allowedRoutes:
        namespaces:
          from: Same
    - name: http-redirect
      protocol: HTTP
      port: $HTTP_PORT
      hostname: "*.${env_host}"
      allowedRoutes:
        namespaces:
          from: Same
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gateway-https-redirect
  namespace: $env_ns
spec:
  parentRefs:
    - name: gateway
      sectionName: http-redirect
  rules:
    - filters:
        - type: RequestRedirect
          requestRedirect:
            scheme: https
            statusCode: 301
EOF
  done
}

configure_cli() {
  [[ -n "$POND_CLI" ]] || return 0
  step "Configuring pond CLI context '$CONTEXT_NAME'"
  "$POND_CLI" context add "$CONTEXT_NAME" "${CLI_CONTEXT_ARGS[@]}" >/dev/null \
    || die "pond context add failed"
  "$POND_CLI" context use "$CONTEXT_NAME" >/dev/null || die "pond context use failed"
}

save_state() {
  STATE_FILE="${HOME}/.pond/setup-${CONTEXT_NAME}.env"
  mkdir -p "${HOME}/.pond"
  ( umask 077
    printf '%s\n' \
      "POND_SERVER_URL=${SERVER_URL}" \
      "POND_ADMIN_KEY=${ADMIN_KEY}" \
      "POND_JWT_SECRET=${JWT_SECRET}" \
      "POND_CLI_TOKEN=${CLI_TOKEN}" \
      "POND_CLUSTER_ID=${CLUSTER_ID}" \
      "POND_PROJECT_ID=${PROJECT_ID}" \
      "POND_KUBECTL_FORWARD_PORT=${FORWARD_PORT}" \
      "POND_KUBE_CONTEXT=${KUBE_CONTEXT}" \
      "POND_KUBE_NAMESPACE=${NAMESPACE}" \
      "POND_KUBE_SERVICE=${SERVER_RELEASE}" \
      > "$STATE_FILE" )
  chmod 600 "$STATE_FILE"
}

# gateway_listener_ready <namespace> — the TLS listener itself is programmed;
# the Gateway's own Programmed condition stays True even when a listener is invalid.
gateway_listener_ready() {
  kc get gateway gateway --namespace "$1" -o json 2>/dev/null | jq -e '
    .status.listeners[]? | select(.name=="http") | .conditions[]?
    | select(.type=="Programmed" and .status=="True")' >/dev/null
}

gateway_address() {
  local addr
  addr=$(kc get gateway gateway --namespace "$1" -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || true)
  if [[ -z "$addr" && "$GATEWAY_CONTROLLER" == "traefik" ]]; then
    addr=$(kc get svc traefik --namespace kube-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)
  fi
  printf '%s' "$addr"
}

# Only warns: issuance and the LoadBalancer address depend on things outside the
# cluster, and both converge on their own once those are fixed.
wait_https() {
  GATEWAY_ADDRESS=""
  [[ "$INSTALL_GATEWAY" == "true" ]] || return 0
  local env env_ns host ready addr hint=""
  [[ "$TLS" == "cloudflare" ]] && hint=" (Let's Encrypt can take a few minutes)"
  step "Waiting for certificates and gateways${hint}"
  for env in "${ENV_LIST[@]}"; do
    env_ns="${NAMESPACE}-${env}"
    host="*.$(env_base_host "$env")"
    if kc wait --namespace "$env_ns" --for=condition=Ready certificate/gateway-tls --timeout 5m >/dev/null 2>&1; then
      ok "certificate for ${host}"
    else
      warn "certificate for ${host} not issued yet (check: kubectl describe certificate/gateway-tls -n $env_ns --context $KUBE_CONTEXT)"
      continue
    fi

    ready="false"; addr=""
    for _ in $(seq 1 60); do
      gateway_listener_ready "$env_ns" && ready="true"
      addr=$(gateway_address "$env_ns")
      [[ "$ready" == "true" && -n "$addr" ]] && break
      sleep 2
    done
    if [[ "$ready" != "true" ]]; then
      warn "gateway in '$env_ns' isn't serving HTTPS yet (check: kubectl describe gateway/gateway -n $env_ns --context $KUBE_CONTEXT)"
    elif [[ -z "$addr" ]]; then
      if [[ "$GATEWAY_CONTROLLER" == "envoy" ]]; then
        warn "the gateway has no external address yet — the cluster needs a LoadBalancer implementation with ports 80/443 free (check: kubectl get svc -n envoy-gateway-system --context $KUBE_CONTEXT)"
      else
        warn "traefik's Service has no external address (check: kubectl get svc traefik -n kube-system --context $KUBE_CONTEXT)"
      fi
    else
      GATEWAY_ADDRESS="$addr"
      ok "gateway for ${host} at ${addr}"
    fi
  done
}

# ── Summary ───────────────────────────────────────────────────────────────────
summary() {
  local env first_env="${ENV_LIST[0]}" todo=0

  echo
  echo "${bold}${green}✔ Pond is set up${reset}"
  echo
  echo "  Cluster        ${KUBE_CONTEXT} (${API_SERVER})"
  echo "  Server         pond-server ${IMAGE_TAG} in namespace ${NAMESPACE}"
  echo "  Project        default"
  for env in "${ENV_LIST[@]}"; do
    if [[ "$INSTALL_GATEWAY" == "true" ]]; then
      echo "  Environment    ${env} → https://<service>.$(env_base_host "$env")"
    else
      echo "  Environment    ${env} (namespace ${NAMESPACE}-${env})"
    fi
  done
  [[ "$INSTALL_POSTGRES" == "true" ]] && echo "  Postgres       managed via CloudNativePG"
  [[ "$INSTALL_GATEWAY" == "true" ]]  && echo "  Certificates   $(tls_description)"
  if [[ -n "$POND_CLI" ]]; then
    echo "  CLI context    ${CONTEXT_NAME} (active) — pond commands open the connection to the server themselves"
  fi

  if [[ "$INSTALL_GATEWAY" == "true" || -z "$POND_CLI" ]]; then
    echo
    echo "${bold}Still to do${reset}"
  fi
  if [[ "$INSTALL_GATEWAY" == "true" ]]; then
    todo=$(( todo + 1 ))
    echo "  ${todo}. Point DNS at the gateway (${GATEWAY_ADDRESS:-address pending — see the warnings above}):"
    for env in "${ENV_LIST[@]}"; do
      echo "       *.$(env_base_host "$env")"
    done
  fi
  if [[ "$TLS" == "self-signed" ]]; then
    todo=$(( todo + 1 ))
    echo "  ${todo}. Trust the private CA on each device that opens your services:"
    echo "       kubectl get secret pond-ca -n cert-manager --context ${KUBE_CONTEXT} \\"
    echo "         -o jsonpath='{.data.ca\\.crt}' | base64 -d > pond-ca.crt"
  fi
  if [[ -z "$POND_CLI" ]]; then
    todo=$(( todo + 1 ))
    echo "  ${todo}. Build the pond CLI (make build-cli in a checkout of the repo), then:"
    echo "       source ${STATE_FILE}"
    echo "       pond context add ${CONTEXT_NAME} --server ${SERVER_URL} --token \"\$POND_CLI_TOKEN\" \\"
    echo "         --project ${PROJECT_ID} --env ${first_env} \\"
    echo "         --kube-context ${KUBE_CONTEXT} --kube-namespace ${NAMESPACE} \\"
    echo "         --kube-service ${SERVER_RELEASE} --kubectl-forward-port ${FORWARD_PORT}"
    echo "       pond context use ${CONTEXT_NAME}"
  fi

  echo
  echo "${bold}Deploy a first service${reset}"
  echo "  Save this as pond.yml in an empty directory:"
  echo
  echo "    version: 1"
  echo "    name: hello"
  echo "    image: traefik/whoami"
  echo "    service:"
  echo "      port: 80"
  if [[ "$INSTALL_GATEWAY" == "true" ]]; then
    echo "    gateway:"
    echo "      http:"
    echo "        enabled: true"
  fi
  echo
  echo "  Then run:  pond deploy --tag latest"
  if [[ "$INSTALL_GATEWAY" == "true" ]]; then
    echo "  It answers at https://hello.$(env_base_host "$first_env")"
  else
    echo "  and check it with:  pond deployment status hello --env ${first_env}"
  fi

  echo
  echo "${bold}Good to know${reset}"
  note "Pond's own Postgres has no volume: after a restart of that pod, re-run this script."
  note "Admin key, JWT secret and CLI token: ${STATE_FILE} (keep it private)."
  note "To remove pond (cluster-wide components stay):"
  echo "      helm uninstall ${SERVER_RELEASE} ${AGENT_RELEASE} -n ${NAMESPACE} --kube-context ${KUBE_CONTEXT}"
  printf '      kubectl delete namespace %s' "$NAMESPACE"
  for env in "${ENV_LIST[@]}"; do printf ' %s-%s' "$NAMESPACE" "$env"; done
  echo " --context ${KUBE_CONTEXT}"
  [[ -n "$POND_CLI" ]] && echo "      pond context remove ${CONTEXT_NAME}"
  return 0
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
  parse_args "$@"
  [[ -z "$FEATURES" ]] || parse_features_flag

  echo
  echo "${bold}Pond homelab setup${reset}"
  echo "──────────────────────────────────────────────────"
  explain "Installs pond's server and agent into a Kubernetes cluster you control, creates" \
          "a project called 'default' with the environments you choose, and points the" \
          "pond CLI at it. Nothing in the cluster changes until you confirm the plan."

  preflight_local
  ask_cluster
  ask_environments
  ask_postgres
  ask_https

  CONTEXT_NAME="${CONTEXT_NAME:-$KUBE_CONTEXT}"
  CLUSTER_NAME="${CLUSTER_NAME:-$KUBE_CONTEXT}"
  FORWARD_PORT="${FORWARD_PORT:-$(random_free_port)}"
  SERVER_URL="http://localhost:${FORWARD_PORT}"
  # Charts are published as the release tag minus the "v"; without a pin Helm
  # skips pre-releases and would pair the image with an older chart.
  if [[ -z "$CHART_VERSION" && "$IMAGE_TAG" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+ ]]; then
    CHART_VERSION="${IMAGE_TAG#v}"
  fi
  HELM_VERSION_FLAG=()
  [[ -z "$CHART_VERSION" ]] || HELM_VERSION_FLAG=(--version "$CHART_VERSION")

  review

  build_cli
  install_server
  install_features
  start_port_forward
  register_cluster
  install_agent
  create_project
  create_environments
  CLI_CONTEXT_ARGS=(
    --server "$SERVER_URL" --token "$CLI_TOKEN" --project "$PROJECT_ID" --env "${ENV_LIST[0]}"
    --kube-context "$KUBE_CONTEXT" --kube-namespace "$NAMESPACE" --kube-service "$SERVER_RELEASE"
    --kubectl-forward-port "$FORWARD_PORT"
  )
  configure_cli
  save_state
  wait_https
  summary
}

main "$@"

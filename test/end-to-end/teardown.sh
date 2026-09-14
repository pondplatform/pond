#!/usr/bin/env bash
# Tears down the full local pond environment created by local-setup.sh.
#
# What it removes:
#   - Helm releases: pond-server, pond-agent (namespace: pond)
#   - Helm release:  cnpg (namespace: cnpg-system)
#   - Helm release:  eg (namespace: envoy-gateway-system) and the GatewayClass
#   - Kubernetes namespaces: pond, e2e-staging, cnpg-system, envoy-gateway-system
#   - Port-forward processes for the server and gateway ports
#   - Local env file: test/end-to-end/.e2e-env
#
# The Gateway API CRDs are left alone: they are cluster-scoped and k3s ships them
# with traefik, so removing them would break unrelated things.
#
# Usage:
#   ./test/end-to-end/teardown.sh
#
# Environment overrides (same defaults as local-setup.sh / gateway-setup.sh):
#   NAMESPACE        default: pond
#   SERVER_RELEASE   default: pond-server
#   AGENT_RELEASE    default: pond-agent
#   SERVER_PORT      default: 8080
#   GATEWAY_NS       default: e2e-staging
#   GATEWAY_NAME     default: gateway
#   GATEWAY_CLASS    default: eg
#   EG_NS            default: envoy-gateway-system
#   EG_RELEASE       default: eg

set -euo pipefail

NAMESPACE="${NAMESPACE:-pond}"
SERVER_RELEASE="${SERVER_RELEASE:-pond-server}"
AGENT_RELEASE="${AGENT_RELEASE:-pond-agent}"
SERVER_PORT="${SERVER_PORT:-8080}"
GATEWAY_LOCAL_PORT="${GATEWAY_LOCAL_PORT:-18080}"
GATEWAY_NS="${GATEWAY_NS:-e2e-staging}"
GATEWAY_NAME="${GATEWAY_NAME:-gateway}"
GATEWAY_CLASS="${GATEWAY_CLASS:-eg}"
EG_NS="${EG_NS:-envoy-gateway-system}"
EG_RELEASE="${EG_RELEASE:-eg}"
CONTEXT="rancher-desktop"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_ENV="${E2E_ENV:-$SCRIPT_DIR/.e2e-env}"

bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "  ${yellow}$*${reset}"; }
warn()  { echo "  ${yellow}⚠ $*${reset}"; }

# ── Sanity checks ─────────────────────────────────────────────────────────────
for cmd in kubectl helm; do
  command -v "$cmd" &>/dev/null || { echo "${bold}${red}✖ $cmd not found in PATH${reset}" >&2; exit 1; }
done

CURRENT_CTX=$(kubectl config current-context 2>/dev/null) \
  || { echo "${bold}${red}✖ kubectl could not read the current context${reset}" >&2; exit 1; }
[[ "$CURRENT_CTX" == "$CONTEXT" ]] \
  || { echo "${bold}${red}✖ current kube context is '$CURRENT_CTX', expected '$CONTEXT'${reset}" >&2; exit 1; }

# ── 1. Kill port-forwards ─────────────────────────────────────────────────────
for port in "$SERVER_PORT" "$GATEWAY_LOCAL_PORT"; do
  step "Stopping port-forward on :$port"
  if pkill -f "kubectl port-forward.*${port}" 2>/dev/null; then
    info "Port-forward stopped"
  else
    info "No matching port-forward process found"
  fi
done

E2E_PIDS="${E2E_PIDS:-$SCRIPT_DIR/.e2e-pids}"
if [[ -f "$E2E_PIDS" ]]; then
  while IFS= read -r pid; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null && info "Stopped port-forward PID $pid" || true
    fi
  done < "$E2E_PIDS"
  rm -f "$E2E_PIDS"
  info "Removed: $E2E_PIDS"
fi

# ── 2. Uninstall Helm releases ────────────────────────────────────────────────
step "Uninstalling Helm releases from namespace '$NAMESPACE'"
for release in "$SERVER_RELEASE" "$AGENT_RELEASE"; do
  if helm status "$release" --namespace "$NAMESPACE" --kube-context "$CONTEXT" &>/dev/null; then
    helm uninstall "$release" --namespace "$NAMESPACE" --kube-context "$CONTEXT"
    info "Uninstalled: $release"
  else
    warn "Release not found (skipping): $release"
  fi
done

step "Uninstalling cnpg from namespace 'cnpg-system'"
if helm status cnpg --namespace cnpg-system --kube-context "$CONTEXT" &>/dev/null; then
  helm uninstall cnpg --namespace cnpg-system --kube-context "$CONTEXT"
  info "Uninstalled: cnpg"
else
  warn "Release not found (skipping): cnpg"
fi

# ── 3. Remove the Gateway API stack ───────────────────────────────────────────
# Ordered deliberately: the controller puts a finalizer on the GatewayClass while
# a Gateway still references it, so the Gateway and the class have to go while the
# controller is still running to reconcile the removal.
step "Deleting Gateway '$GATEWAY_NAME' and GatewayClass '$GATEWAY_CLASS'"
if kubectl get gateway "$GATEWAY_NAME" -n "$GATEWAY_NS" --context "$CONTEXT" &>/dev/null; then
  kubectl delete gateway "$GATEWAY_NAME" -n "$GATEWAY_NS" --context "$CONTEXT"
  info "Deleted Gateway: $GATEWAY_NS/$GATEWAY_NAME"
else
  warn "Gateway not found (skipping): $GATEWAY_NS/$GATEWAY_NAME"
fi
if kubectl get gatewayclass "$GATEWAY_CLASS" --context "$CONTEXT" &>/dev/null; then
  kubectl delete gatewayclass "$GATEWAY_CLASS" --context "$CONTEXT"
  info "Deleted GatewayClass: $GATEWAY_CLASS"
else
  warn "GatewayClass not found (skipping): $GATEWAY_CLASS"
fi

step "Uninstalling $EG_RELEASE from namespace '$EG_NS'"
if helm status "$EG_RELEASE" --namespace "$EG_NS" --kube-context "$CONTEXT" &>/dev/null; then
  helm uninstall "$EG_RELEASE" --namespace "$EG_NS" --kube-context "$CONTEXT"
  info "Uninstalled: $EG_RELEASE"
else
  warn "Release not found (skipping): $EG_RELEASE"
fi

# ── 4. Delete namespaces ──────────────────────────────────────────────────────
step "Deleting namespaces"
for ns in "$NAMESPACE" "e2e-staging" "cnpg-system" "$EG_NS"; do
  if kubectl get namespace "$ns" --context "$CONTEXT" &>/dev/null; then
    kubectl delete namespace "$ns" --context "$CONTEXT"
    info "Deleted namespace: $ns"
  else
    warn "Namespace not found (skipping): $ns"
  fi
done

# ── 5. Remove env file ────────────────────────────────────────────────────────
step "Removing env file"
if [[ -f "$E2E_ENV" ]]; then
  rm -f "$E2E_ENV"
  info "Removed: $E2E_ENV"
else
  warn "Env file not found (skipping): $E2E_ENV"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
step "Done — pond namespace fully torn down"

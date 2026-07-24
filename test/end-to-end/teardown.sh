#!/usr/bin/env bash
# Tears down the full local pond environment created by local-setup.sh.
#
# What it removes:
#   - Helm releases: pond-server, pond-agent (namespace: pond)
#   - Helm release:  cnpg (namespace: cnpg-system)
#   - Kubernetes namespaces: pond, e2e-staging, cnpg-system
#   - Port-forward processes for the server port
#   - Local env file: test/end-to-end/.e2e-env
#
# Usage:
#   ./test/end-to-end/teardown.sh
#
# Environment overrides (same defaults as local-setup.sh):
#   NAMESPACE        default: pond
#   SERVER_RELEASE   default: pond-server
#   AGENT_RELEASE    default: pond-agent
#   SERVER_PORT      default: 8080

set -euo pipefail

NAMESPACE="${NAMESPACE:-pond}"
SERVER_RELEASE="${SERVER_RELEASE:-pond-server}"
AGENT_RELEASE="${AGENT_RELEASE:-pond-agent}"
SERVER_PORT="${SERVER_PORT:-8080}"
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

# ── 1. Kill port-forward ──────────────────────────────────────────────────────
step "Stopping port-forward on :$SERVER_PORT"
if pkill -f "kubectl port-forward.*${SERVER_PORT}" 2>/dev/null; then
  info "Port-forward stopped"
else
  info "No matching port-forward process found"
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

# ── 3. Delete namespaces ──────────────────────────────────────────────────────
step "Deleting namespaces"
for ns in "$NAMESPACE" "e2e-staging" "cnpg-system"; do
  if kubectl get namespace "$ns" --context "$CONTEXT" &>/dev/null; then
    kubectl delete namespace "$ns" --context "$CONTEXT"
    info "Deleted namespace: $ns"
  else
    warn "Namespace not found (skipping): $ns"
  fi
done

# ── 4. Remove env file ────────────────────────────────────────────────────────
step "Removing env file"
if [[ -f "$E2E_ENV" ]]; then
  rm -f "$E2E_ENV"
  info "Removed: $E2E_ENV"
else
  warn "Env file not found (skipping): $E2E_ENV"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
step "Done — pond namespace fully torn down"

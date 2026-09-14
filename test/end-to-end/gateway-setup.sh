#!/usr/bin/env bash
# Provisions the Gateway API prerequisites that deploy-gateway.sh needs:
#
#   1. Gateway API CRDs (only if the cluster does not already have them)
#   2. Envoy Gateway as the controller
#   3. A GatewayClass pointing at that controller
#   4. A Gateway named 'gateway' with a listener named 'http' in the target
#      namespace — that is what base-service's HTTPRoute parentRefs point to
#
# In production this is the cluster operator's job; see docs/operators/production.md.
#
# Idempotent — safe to re-run.
#
# Usage:
#   ./test/end-to-end/gateway-setup.sh

set -euo pipefail

GATEWAY_NS="${GATEWAY_NS:-e2e-staging}"
GATEWAY_NAME="${GATEWAY_NAME:-gateway}"
GATEWAY_CLASS="${GATEWAY_CLASS:-eg}"
EG_NS="${EG_NS:-envoy-gateway-system}"
EG_RELEASE="${EG_RELEASE:-eg}"
EG_VERSION="${EG_VERSION:-v1.5.5}"
CONTEXT="rancher-desktop"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

# ── Sanity checks ─────────────────────────────────────────────────────────────
for cmd in kubectl helm; do
  command -v "$cmd" &>/dev/null || die "$cmd not found in PATH"
done

CURRENT_CTX=$(kubectl config current-context 2>/dev/null) \
  || die "kubectl could not read the current context"
[[ "$CURRENT_CTX" == "$CONTEXT" ]] \
  || die "current kube context is '$CURRENT_CTX', expected '$CONTEXT'"

CHART_DIR=$(mktemp -d /tmp/pond-gateway-helm.XXXXXX)
trap 'rm -rf "$CHART_DIR"' EXIT

step "Fetching gateway-helm $EG_VERSION"
helm pull "oci://docker.io/envoyproxy/gateway-helm" \
  --version "$EG_VERSION" --untar --untardir "$CHART_DIR" \
  || die "could not pull gateway-helm $EG_VERSION"
CHART="$CHART_DIR/gateway-helm"

# ── 1. Gateway API CRDs ───────────────────────────────────────────────────────
# k3s ships these already (traefik bundles them), and they are cluster-scoped and
# shared, so reapplying them would fight whichever manager owns them. Install only
# when genuinely absent.
step "Ensuring Gateway API CRDs are installed"
if kubectl get crd httproutes.gateway.networking.k8s.io --context "$CONTEXT" &>/dev/null; then
  info "Already present — leaving them to their current owner"
else
  kubectl apply --server-side -f "$CHART/crds/gatewayapi-crds.yaml" --context "$CONTEXT" \
    || die "could not install the Gateway API CRDs"
fi

# ── 2. Envoy Gateway controller ───────────────────────────────────────────────
# --skip-crds plus a separate apply of the chart's own CRDs: letting Helm manage
# crds/ would make it claim the shared Gateway API CRDs from step 1 and fail with
# an ownership conflict.
step "Installing Envoy Gateway into namespace '$EG_NS'"
kubectl apply --server-side -f "$CHART/crds/generated/" --context "$CONTEXT" \
  || die "could not install the Envoy Gateway CRDs"

helm upgrade --install "$EG_RELEASE" "$CHART" \
  --namespace "$EG_NS" \
  --kube-context "$CONTEXT" \
  --create-namespace \
  --skip-crds \
  --wait --timeout 3m \
  || die "helm upgrade failed for $EG_RELEASE (check: kubectl logs -n $EG_NS -l control-plane=envoy-gateway)"

# ── 3. GatewayClass ───────────────────────────────────────────────────────────
step "Creating GatewayClass '$GATEWAY_CLASS'"
kubectl apply --context "$CONTEXT" -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: $GATEWAY_CLASS
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
EOF

# ── 4. Gateway ────────────────────────────────────────────────────────────────
step "Creating Gateway '$GATEWAY_NAME' in namespace '$GATEWAY_NS'"
kubectl create namespace "$GATEWAY_NS" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply --context "$CONTEXT" -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: $GATEWAY_NAME
  namespace: $GATEWAY_NS
spec:
  gatewayClassName: $GATEWAY_CLASS
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
EOF

# Accepted, not Programmed: k3s cannot hand out a second LoadBalancer address on
# :80 while traefik holds it, so Programmed stays false. The data plane still runs
# and is reachable on its ClusterIP, which is all the scenario needs.
step "Waiting for Gateway '$GATEWAY_NAME' to be accepted"
kubectl wait --for=condition=Accepted "gateway/$GATEWAY_NAME" \
  --namespace "$GATEWAY_NS" --context "$CONTEXT" --timeout 2m \
  || die "Gateway '$GATEWAY_NAME' was not accepted (check: kubectl describe gateway/$GATEWAY_NAME -n $GATEWAY_NS)"

step "Done — Gateway API prerequisites ready"

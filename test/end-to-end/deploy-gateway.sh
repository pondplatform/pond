#!/usr/bin/env bash
# End-to-end scenario: deploy a service with gateway.http enabled and verify a
# real Gateway API controller accepts the resulting HTTPRoute and routes to it.
#
# What the route contains is checked by the integration tests, which render
# the same pond.yml; this only covers what takes a real controller.
#
# Flow:
#   1. Submit deployment (no dependencies, so it runs straight through)
#   2. Assert the controller Accepted the route and resolved its refs
#   3. Send a request through the gateway data plane to the derived hostname
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env and the Gateway)
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-gateway.sh

set -euo pipefail

SERVICE_NAME="gateway-demo"
GATEWAY_NS="${GATEWAY_NS:-e2e-staging}"
GATEWAY_NAME="${GATEWAY_NAME:-gateway}"
# The gateway data plane is reachable directly: on Rancher Desktop traefik owns the
# cluster's :80, so no port-forward is needed to reach the listener.
GATEWAY_URL="${GATEWAY_URL:-http://127.0.0.1}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

HOST="$SERVICE_NAME.$INGRESS_BASE_HOST"

# A cluster may run more than one Gateway API controller, and every one of them
# writes its own status.parents entry for the same parentRef. Resolve the one that
# actually owns this Gateway so the route assertions below cannot pass or fail on
# whichever entry happens to be listed first.
GATEWAY_CLASS=$(kubectl get gateway "$GATEWAY_NAME" -n "$GATEWAY_NS" \
  -o jsonpath='{.spec.gatewayClassName}') \
  || die "Gateway $GATEWAY_NS/$GATEWAY_NAME not found — run local-setup.sh first"
GATEWAY_CONTROLLER=$(kubectl get gatewayclass "$GATEWAY_CLASS" \
  -o jsonpath='{.spec.controllerName}') \
  || die "GatewayClass '$GATEWAY_CLASS' not found"

FAILED=0
expect_eq() {
  local what="$1" want="$2" got="$3"
  if [[ "$want" == "$got" ]]; then
    info "ok: $what = $got"
  else
    warn "MISMATCH: $what — expected '$want', got '$got'"
    FAILED=1
  fi
}

# route_condition <route_json> <type> — prints the condition status as written by
# $GATEWAY_CONTROLLER, or nothing if it has not written status yet.
route_condition() {
  jq -r --arg t "$2" --arg c "$GATEWAY_CONTROLLER" \
    '[.status.parents[]? | select(.controllerName==$c) | .conditions[]?
      | select(.type==$t) | .status] | first // ""' <<<"$1"
}

# gw_status <host> <path> — prints the HTTP status of a request through the gateway
gw_status() {
  curl -s -o /dev/null -w '%{http_code}' -H "Host: $1" "$GATEWAY_URL$2"
}

# ── Submit ────────────────────────────────────────────────────────────────────
step "Submitting $SERVICE_NAME deployment (tag: $IMAGE_TAG)"
DEPLOY_OUTPUT=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  "$POND" deploy --no-wait \
    --pond-file "$SCRIPT_DIR/../test-data/gateway/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" 2>&1)
echo "$DEPLOY_OUTPUT"

DEPLOYMENT_ID=$(get_deployment_id "$DEPLOY_OUTPUT")
info "Deployment ID: $DEPLOYMENT_ID"

step "Waiting for deployment to complete"
poll_status "$DEPLOYMENT_ID" "succeeded" 150 2

# ── Controller acceptance ─────────────────────────────────────────────────────
# Helm creates the route and the workload together, so the deployment can report
# succeeded a beat before the controller has written status.parents. Re-read
# until the conditions land rather than asserting against a status-less snapshot.
step "Waiting for the controller to reconcile the HTTPRoute"
ROUTE=""
for i in $(seq 1 30); do
  ROUTE=$(kubectl get httproute "$SERVICE_NAME" -n "$GATEWAY_NS" -o json 2>/dev/null) || ROUTE=""
  if [[ -n "$ROUTE" && "$(route_condition "$ROUTE" Accepted)" == "True" ]]; then
    break
  fi
  info "[$i/30] HTTPRoute not reconciled yet — retrying in 2s"
  sleep 2
done
[[ -n "$ROUTE" ]] || die "HTTPRoute/$SERVICE_NAME was not created"

step "Checking the controller accepted the route"
expect_eq "Accepted condition" "True" "$(route_condition "$ROUTE" Accepted)"
expect_eq "ResolvedRefs condition" "True" "$(route_condition "$ROUTE" ResolvedRefs)"

# ── Traffic ───────────────────────────────────────────────────────────────────
# A route reports Accepted before its config has reached the data plane, so wait
# for the proxy to actually answer rather than assuming it already does.
step "Waiting for the gateway at $GATEWAY_URL to route to $HOST"
ROUTING=0
for i in $(seq 1 30); do
  if [[ "$(gw_status "$HOST" /)" == "200" ]]; then
    ROUTING=1
    break
  fi
  info "[$i/30] not routing yet — retrying in 2s"
  sleep 2
done
[[ $ROUTING -eq 1 ]] || die "gateway never served $HOST at $GATEWAY_URL"
info "ok: GET / on $HOST answered 200"

[[ $FAILED -eq 0 ]] || die "one or more gateway assertions failed"

step "Done — HTTPRoute accepted and serving traffic"

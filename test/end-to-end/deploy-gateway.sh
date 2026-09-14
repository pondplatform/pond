#!/usr/bin/env bash
# End-to-end scenario: deploy a service with gateway.http enabled and verify the
# resulting Gateway API HTTPRoute actually carries traffic.
#
# Flow:
#   1. Submit deployment (no dependencies, so it runs straight through)
#   2. Assert deployment.replicas reached the Kubernetes Deployment
#   3. Assert the HTTPRoute spec: derived hostname, platform-owned parentRefs and
#      backendRefs, and the user's matches/filters
#   4. Assert the route was Accepted with resolved refs by the controller
#   5. Send real requests through the gateway data plane and check that the
#      URLRewrite and RequestHeaderModifier filters took effect, and that a
#      non-matching Host is not served
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#   - gateway-setup.sh has been run (Gateway API CRDs, controller, Gateway)
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-gateway.sh

set -euo pipefail

SERVICE_NAME="gateway-demo"
GATEWAY_NS="${GATEWAY_NS:-e2e-staging}"
GATEWAY_NAME="${GATEWAY_NAME:-gateway}"
EG_NS="${EG_NS:-envoy-gateway-system}"
LOCAL_PORT="${GATEWAY_LOCAL_PORT:-18080}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

HOST="$SERVICE_NAME.$INGRESS_BASE_HOST"
BODY_FILE=$(mktemp /tmp/pond-gateway-body.XXXXXX)
PF_PID=""
cleanup() {
  [[ -n "$PF_PID" ]] && kill "$PF_PID" 2>/dev/null || true
  rm -f "$BODY_FILE"
}
trap cleanup EXIT

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

# gw_status <host> <path> — prints the HTTP status; the body lands in $BODY_FILE
gw_status() {
  curl -s -o "$BODY_FILE" -w '%{http_code}' \
    -H "Host: $1" "http://127.0.0.1:$LOCAL_PORT$2"
}

# ── Submit ────────────────────────────────────────────────────────────────────
step "Submitting $SERVICE_NAME deployment (tag: $IMAGE_TAG)"
DEPLOY_OUTPUT=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  "$POND" deploy \
    --config "$SCRIPT_DIR/../test-data/gateway/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$IMAGE_TAG" 2>&1)
echo "$DEPLOY_OUTPUT"

DEPLOYMENT_ID=$(get_deployment_id "$DEPLOY_OUTPUT")
info "Deployment ID: $DEPLOYMENT_ID"

step "Waiting for deployment to complete"
poll_status "$DEPLOYMENT_ID" "succeeded" 150 2

# ── deployment.replicas ───────────────────────────────────────────────────────
step "Checking deployment.replicas reached the workload"
expect_eq "Deployment replicas" "1" \
  "$(kubectl get deployment "$SERVICE_NAME" -n "$GATEWAY_NS" -o jsonpath='{.spec.replicas}')"

# ── HTTPRoute spec ────────────────────────────────────────────────────────────
step "Checking the HTTPRoute spec"
ROUTE=$(kubectl get httproute "$SERVICE_NAME" -n "$GATEWAY_NS" -o json) \
  || die "HTTPRoute/$SERVICE_NAME was not created"

expect_eq "hostnames" "$HOST" \
  "$(jq -r '.spec.hostnames | join(",")' <<<"$ROUTE")"
expect_eq "parentRefs" "$GATEWAY_NAME/http" \
  "$(jq -r '.spec.parentRefs[0] | "\(.name)/\(.sectionName)"' <<<"$ROUTE")"
expect_eq "rule count" "2" \
  "$(jq -r '.spec.rules | length' <<<"$ROUTE")"
expect_eq "rule 0 path match" "PathPrefix /rewritten" \
  "$(jq -r '.spec.rules[0].matches[0].path | "\(.type) \(.value)"' <<<"$ROUTE")"
expect_eq "rule 0 filters" "URLRewrite,RequestHeaderModifier" \
  "$(jq -r '[.spec.rules[0].filters[].type] | join(",")' <<<"$ROUTE")"
expect_eq "rule 0 rewrite" "ReplacePrefixMatch /" \
  "$(jq -r '.spec.rules[0].filters[0].urlRewrite.path | "\(.type) \(.replacePrefixMatch)"' <<<"$ROUTE")"
expect_eq "rule 1 path match" "PathPrefix /" \
  "$(jq -r '.spec.rules[1].matches[0].path | "\(.type) \(.value)"' <<<"$ROUTE")"
# backendRefs are supplied by the chart, never by pond.yml.
expect_eq "backendRefs on every rule" "$SERVICE_NAME:80 $SERVICE_NAME:80" \
  "$(jq -r '[.spec.rules[].backendRefs[0] | "\(.name):\(.port)"] | join(" ")' <<<"$ROUTE")"

# ── Controller acceptance ─────────────────────────────────────────────────────
step "Checking the controller accepted the route"
expect_eq "Accepted condition" "True" \
  "$(jq -r '.status.parents[0].conditions[] | select(.type=="Accepted") | .status' <<<"$ROUTE")"
expect_eq "ResolvedRefs condition" "True" \
  "$(jq -r '.status.parents[0].conditions[] | select(.type=="ResolvedRefs") | .status' <<<"$ROUTE")"

# ── Traffic ───────────────────────────────────────────────────────────────────
step "Port-forwarding the gateway data plane to localhost:$LOCAL_PORT"
ENVOY_SVC=$(kubectl get svc -n "$EG_NS" \
  -l "gateway.envoyproxy.io/owning-gateway-name=$GATEWAY_NAME,gateway.envoyproxy.io/owning-gateway-namespace=$GATEWAY_NS" \
  -o jsonpath='{.items[0].metadata.name}')
[[ -n "$ENVOY_SVC" ]] || die "no envoy service found for Gateway $GATEWAY_NS/$GATEWAY_NAME"
info "Data plane service: $ENVOY_SVC"

kubectl port-forward -n "$EG_NS" "svc/$ENVOY_SVC" "${LOCAL_PORT}:80" &
PF_PID=$!

# A route reports Accepted before its config has reached the data plane, so wait
# for the proxy to actually answer rather than assuming it already does.
step "Waiting for the gateway to route to $HOST"
ROUTING=0
for i in $(seq 1 30); do
  if [[ "$(gw_status "$HOST" /)" == "200" ]]; then
    ROUTING=1
    break
  fi
  info "[$i/30] not routing yet — retrying in 2s"
  sleep 2
done
[[ $ROUTING -eq 1 ]] || die "gateway never served $HOST on :$LOCAL_PORT"

step "Checking requests through the gateway"

# The catch-all rule has no filters, so the injected header must be absent here.
expect_eq "GET / status" "200" "$(gw_status "$HOST" /)"
if grep -qi 'X-Pond-Filter' "$BODY_FILE"; then
  warn "MISMATCH: catch-all rule injected X-Pond-Filter but has no filters"
  FAILED=1
else
  info "ok: catch-all rule injected no header"
fi

# /rewritten/echo hits the first rule: URLRewrite strips the prefix and
# RequestHeaderModifier adds a header, both observable in whoami's echo.
expect_eq "GET /rewritten/echo status" "200" "$(gw_status "$HOST" /rewritten/echo)"
if grep -q 'GET /echo ' "$BODY_FILE"; then
  info "ok: URLRewrite rewrote /rewritten/echo to /echo"
else
  warn "MISMATCH: URLRewrite did not rewrite the path — backend saw:"
  grep -E '^(GET|POST) ' "$BODY_FILE" || true
  FAILED=1
fi
if grep -qi '^X-Pond-Filter: applied' "$BODY_FILE"; then
  info "ok: RequestHeaderModifier added X-Pond-Filter"
else
  warn "MISMATCH: RequestHeaderModifier did not add X-Pond-Filter"
  FAILED=1
fi

# hostnames scopes the route: another Host must not reach this service.
expect_eq "non-matching Host status" "404" "$(gw_status "nope.$INGRESS_BASE_HOST" /)"

[[ $FAILED -eq 0 ]] || die "one or more gateway assertions failed"

step "Done — HTTPRoute created, accepted and serving traffic"

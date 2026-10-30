#!/usr/bin/env bash
# End-to-end scenario: a service depends on another and reaches it at the
# address it imported.
#
# What the consumer imports, and the submits that can't import, are checked by
# the integration tests, starting from the same pond.yml files; this covers what
# takes a real cluster: the imported in-cluster URL actually reaches the provider.
#
# Flow:
#   1. Deploy the provider (whoami on port 8080, behind the gateway)
#   2. Deploy the consumer, which imports the provider's addresses into its env
#   3. Assert the env holds them, and the imported URL answers from the consumer's pod
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-service-dependency.sh

set -euo pipefail

# The names test-data/service-dependency/*.yml deploy as.
PROVIDER="dep-provider"
CONSUMER="dep-consumer"
NAMESPACE="${NAMESPACE:-e2e-staging}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

# deploy <file> <tag> — submits a test-data file and waits for it to succeed.
deploy() {
  local output
  output=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
    "$POND" deploy --no-wait \
      --pond-file "$SCRIPT_DIR/../test-data/service-dependency/$1" \
      --project "$PROJECT_ID" \
      --env staging \
      --tag "$2" 2>&1) || die "submit of $1 failed: $output"
  echo "$output"
  poll_status "$(get_deployment_id "$output")" "succeeded" 150 2
}

# the_pod <service> — prints the service's pod once the one it replaced has
# gone, so exec never lands in a pod that is shutting down.
the_pod() {
  local names
  for _ in $(seq 1 60); do
    names=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$1" \
      -o jsonpath='{.items[*].metadata.name}')
    if [[ -n "$names" && "$names" != *" "* ]]; then
      echo "$names"
      return
    fi
    sleep 2
  done
  die "expected one $1 pod, got '$names'"
}

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

# ── 1–2. Deploy ───────────────────────────────────────────────────────────────
step "Deploying $PROVIDER"
deploy 1-provider.yml latest
step "Deploying $CONSUMER, which depends on $PROVIDER"
deploy 2-consumer.yml alpine

# ── 3. Imported values ────────────────────────────────────────────────────────
POD=$(the_pod "$CONSUMER")
env_of() { kubectl exec -n "$NAMESPACE" "$POD" -- printenv "$1"; }

step "Checking the consumer's env holds the provider's addresses"
HOST="$PROVIDER.$NAMESPACE.svc.cluster.local"
expect_eq PROVIDER_HOST "$HOST" "$(env_of PROVIDER_HOST)"
expect_eq PROVIDER_PORT "8080" "$(env_of PROVIDER_PORT)"
expect_eq PROVIDER_URL "http://$HOST:8080" "$(env_of PROVIDER_URL)"
expect_eq PROVIDER_PUBLIC_HOST "$PROVIDER.$INGRESS_BASE_HOST" "$(env_of PROVIDER_PUBLIC_HOST)"
expect_eq PROVIDER_PUBLIC_URL "http://$PROVIDER.$INGRESS_BASE_HOST" "$(env_of PROVIDER_PUBLIC_URL)"

step "Calling the provider at the imported URL from the consumer's pod"
# whoami echoes the request, including the Hostname of the pod that answered.
ANSWER=$(kubectl exec -n "$NAMESPACE" "$POD" -- sh -c 'wget -qO- "$PROVIDER_URL"') \
  || die "the consumer could not reach the provider at its imported URL"
[[ "$ANSWER" == *"Hostname: $PROVIDER-"* ]] || die "expected whoami of $PROVIDER to answer, got: $ANSWER"
info "ok: $PROVIDER answered at the imported URL"

[[ $FAILED -eq 0 ]] || die "one or more imported values were wrong"

step "Done — the consumer reaches the provider at the address it imported"

#!/usr/bin/env bash
# End-to-end scenario: pond service delete removes what the deploy scenarios
# created, and keeps what it promises to keep.
#
# Runs after the deploy-* scenarios and deletes the services they left behind:
#   1. postgres-demo: its release, its CNPG cluster with that cluster's claims,
#      and its credentials Secret are gone
#   2. volumes-demo: its release is gone, but its volume's claim is kept
#   3. dep-provider can't be deleted while dep-consumer imports it; deleting the
#      consumer first, then the provider, works
#
# What is deleted, and in which order, is checked by the integration tests;
# this covers what takes a real cluster: helm uninstall and tofu destroy
# actually remove the resources.
#
# Prerequisites:
#   - local-setup.sh and the deploy-* scenarios have been run
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/delete-service.sh

set -euo pipefail

NAMESPACE="${NAMESPACE:-e2e-staging}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

# pond service delete deletes from every environment and refuses POND_ENV,
# which load_env exports, so it runs without it.
pond_delete() {
  env -u POND_ENV POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
    "$POND" service delete "$1" --project "$PROJECT_ID" --yes --wait
}

# gone <what> <kubectl get args...> — dies unless kubectl finds nothing,
# polling for a minute: objects owned by a deleted one are collected after it.
gone() {
  local what="$1" found i
  shift
  for i in $(seq 1 30); do
    found=$(kubectl get -n "$NAMESPACE" "$@" -o name 2>/dev/null || true)
    if [[ -z "$found" ]]; then
      info "ok: $what is gone"
      return 0
    fi
    sleep 2
  done
  die "expected $what to be gone, still have: $found"
}

# ── 1. A managed dependency ───────────────────────────────────────────────────
step "Deleting postgres-demo, with its managed postgres"
pond_delete postgres-demo
gone "the postgres-demo release" deployments -l app.kubernetes.io/instance=postgres-demo
gone "the CNPG cluster" clusters.postgresql.cnpg.io staging-postgres-demo-db
gone "the credentials Secret" secrets staging-postgres-demo-db-app-user
gone "the database's claims" pvc -l cnpg.io/cluster=staging-postgres-demo-db

# ── 2. A volume ───────────────────────────────────────────────────────────────
step "Deleting volumes-demo, whose volume is kept"
pond_delete volumes-demo
gone "the volumes-demo release" deployments -l app.kubernetes.io/instance=volumes-demo
CLAIMS=$(kubectl get pvc -n "$NAMESPACE" -l app.kubernetes.io/instance=volumes-demo -o name)
[[ -n "$CLAIMS" ]] || die "expected the volume's claim to be kept"
info "ok: kept $CLAIMS"

# ── 3. A service another one depends on ──────────────────────────────────────
step "Deleting dep-provider while dep-consumer imports it"
if OUTPUT=$(pond_delete dep-provider 2>&1); then
  die "expected the delete to be refused, got: $OUTPUT"
fi
[[ "$OUTPUT" == *"dependency of dep-consumer in staging"* ]] \
  || die "expected the refusal to name dep-consumer, got: $OUTPUT"
info "ok: refused, naming dep-consumer"

step "Deleting dep-consumer, then dep-provider"
pond_delete dep-consumer
pond_delete dep-provider
gone "the dep-consumer release" deployments -l app.kubernetes.io/instance=dep-consumer
gone "the dep-provider release" deployments -l app.kubernetes.io/instance=dep-provider

step "Done — the deleted services left only their volumes behind"

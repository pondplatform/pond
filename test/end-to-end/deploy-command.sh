#!/usr/bin/env bash
# End-to-end scenario: command replaces the image's CMD and keeps its
# ENTRYPOINT, with templates rendered and every $ reaching the process as written.
#
# The rendered args, removing the command and rejecting unsafe ones are checked
# by the integration tests, starting from the same pond.yml; this covers what
# takes a real cluster: the container actually runs the command that way.
#
# Flow:
#   1. Deploy with a command: the entrypoint runs it, {{service.port}} is
#      rendered, and the shell sees $$ rather than the $ Kubernetes would make of it
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-command.sh

set -euo pipefail

# The name test-data/command/pond.yml deploys as.
SERVICE_NAME="command-demo"
NAMESPACE="${NAMESPACE:-e2e-staging}"
# nginx's entrypoint runs whatever command it is given, and the image has a shell.
COMMAND_IMAGE_TAG="${COMMAND_IMAGE_TAG:-alpine}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

# the_pod — prints the service's pod once the one it replaced has gone, so
# exec never lands in a pod that is shutting down.
the_pod() {
  local names
  for _ in $(seq 1 60); do
    names=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$SERVICE_NAME" \
      -o jsonpath='{.items[*].metadata.name}')
    if [[ -n "$names" && "$names" != *" "* ]]; then
      echo "$names"
      return
    fi
    sleep 2
  done
  die "expected one $SERVICE_NAME pod, got '$names'"
}

# ── 1. With a command ─────────────────────────────────────────────────────────
step "Deploying $SERVICE_NAME with a command"
OUTPUT=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
  "$POND" deploy \
    --pond-file "$SCRIPT_DIR/../test-data/command/pond.yml" \
    --project "$PROJECT_ID" \
    --env staging \
    --tag "$COMMAND_IMAGE_TAG" 2>&1) || die "submit failed: $OUTPUT"
echo "$OUTPUT"
poll_status "$(get_deployment_id "$OUTPUT")" "succeeded" 150 2

# The entrypoint execs the command, so the shell is PID 1.
GOT=$(kubectl exec -n "$NAMESPACE" "$(the_pod)" -- cat /tmp/started)
[[ "$GOT" == "port 80, pid 1" ]] || die "what the command wrote — expected 'port 80, pid 1', got '$GOT'"
info "ok: the command wrote '$GOT'"

step "Done — the command ran under the image's entrypoint, verbatim"

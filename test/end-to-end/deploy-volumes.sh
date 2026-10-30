#!/usr/bin/env bash
# End-to-end scenario: a volume keeps a service's data when a redeploy replaces
# its pod.
#
# What each deploy renders (strategy, claim, mount, keep policy) and removing a
# volume are checked by the integration tests, starting from the same files;
# this covers what takes a real cluster: the claim binds, the Recreate rollout
# hands it to the new pod, and the data is still there.
#
# Flow:
#   1. Deploy with a volume and write a marker file to it
#   2. Redeploy with a changed env var: a new pod, and the marker survives
#
# The claim is left behind on purpose, as data always is; teardown.sh removes it
# with the namespace.
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-volumes.sh

set -euo pipefail

# The name every case file in test-data/volumes deploys as.
SERVICE_NAME="volumes-demo"
NAMESPACE="${NAMESPACE:-e2e-staging}"
# nginx:alpine runs a long-lived process and has a shell to read and write the
# volume with; the published sample images have neither.
VOLUME_IMAGE_TAG="${VOLUME_IMAGE_TAG:-alpine}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

# Each case sets its own REVISION in env, so every deploy rolls a new pod.
CASES="$SCRIPT_DIR/../test-data/volumes"
# Unique per run: a claim kept from an earlier run must not pass for this one.
MARKER="written-by-run-$$-$(date +%s)"

# deploy <pond_file> — submits and waits for the deployment to succeed
deploy() {
  local output id
  output=$(POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
    "$POND" deploy --no-wait \
      --pond-file "$1" \
      --project "$PROJECT_ID" \
      --env staging \
      --tag "$VOLUME_IMAGE_TAG" 2>&1) || die "submit failed: $output"
  echo "$output"
  id=$(get_deployment_id "$output")
  poll_status "$id" "succeeded" 150 2
}

pods() {
  kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/instance=$SERVICE_NAME" \
    --field-selector=status.phase=Running -o jsonpath='{.items[*].metadata.name}'
}

in_pod() {
  kubectl exec -n "$NAMESPACE" "$(pods)" -- sh -c "$1"
}

# ── 1. With a volume ──────────────────────────────────────────────────────────
step "Deploying $SERVICE_NAME with a volume"
deploy "$CASES/1-with-volume.yml"
in_pod "echo '$MARKER' > /data/marker"
info "wrote $MARKER to /data/marker"

# ── 2. Redeploy ───────────────────────────────────────────────────────────────
step "Redeploying with a changed env var"
BEFORE=$(pods)
deploy "$CASES/2-changed-env.yml"
AFTER=$(pods)
[[ -n "$AFTER" && "$AFTER" != "$BEFORE" ]] \
  || die "expected a new pod, had '$BEFORE', now '$AFTER'"
info "ok: pod replaced ($BEFORE → $AFTER)"
GOT=$(in_pod 'cat /data/marker')
[[ "$GOT" == "$MARKER" ]] || die "marker after redeploy — expected '$MARKER', got '$GOT'"
info "ok: marker after redeploy = $GOT"

step "Done — the volume kept its data through a redeploy"

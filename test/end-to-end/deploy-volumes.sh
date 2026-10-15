#!/usr/bin/env bash
# End-to-end scenario: a volume keeps a service's data across redeploys, and
# across being removed and added again.
#
# Flow:
#   1. Deploy without a volume, so the release starts out on RollingUpdate
#   2. Add a volume: the rollout switches to Recreate; write a marker file to it
#   3. Redeploy with a changed env var: a new pod, and the marker survives
#   4. Remove the volume: the claim is kept
#   5. Add it again: the claim is adopted and the marker is back
#   6. Submit a smaller size: rejected before anything is deployed
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

SERVICE_NAME="volumes-demo"
NAMESPACE="${NAMESPACE:-e2e-staging}"
CLAIM="$SERVICE_NAME-data"
# nginx:alpine runs a long-lived process and has a shell to read and write the
# volume with; the published sample images have neither.
VOLUME_IMAGE_TAG="${VOLUME_IMAGE_TAG:-alpine}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

WORK_DIR=$(mktemp -d /tmp/pond-volumes.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT
# Unique per run: a claim kept from an earlier run must not pass for this one.
MARKER="written-by-run-$$-$(date +%s)"

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

# pond_yml <revision> [size] — writes a pond.yml, with a volume of [size] if given,
# and prints its path. The revision lands in env, so each one rolls a new pod.
pond_yml() {
  local revision="$1" size="${2:-}" file="$WORK_DIR/pond-$1.yml"
  cat >"$file" <<EOF
version: 1
name: $SERVICE_NAME
image: nginx
service:
  port: 80
env:
  REVISION: "$revision"
EOF
  if [[ -n "$size" ]]; then
    cat >>"$file" <<EOF
volumes:
  data:
    type: shared-single-node
    mountPath: /data
    size: $size
EOF
  fi
  echo "$file"
}

submit() {
  POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
    "$POND" deploy \
      --pond-file "$1" \
      --project "$PROJECT_ID" \
      --env staging \
      --tag "$VOLUME_IMAGE_TAG" 2>&1
}

# deploy <pond_file> — submits and waits for the deployment to succeed
deploy() {
  local output id
  output=$(submit "$1") || die "submit failed: $output"
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

deployment_field() {
  kubectl get deployment "$SERVICE_NAME" -n "$NAMESPACE" -o jsonpath="$1"
}

# ── 1. No volume ──────────────────────────────────────────────────────────────
step "Deploying $SERVICE_NAME without a volume"
deploy "$(pond_yml 1)"
expect_eq "strategy without volumes" "RollingUpdate" "$(deployment_field '{.spec.strategy.type}')"

# ── 2. Add a volume ───────────────────────────────────────────────────────────
step "Adding a volume to the running service"
deploy "$(pond_yml 2 64Mi)"
expect_eq "strategy with a volume" "Recreate" "$(deployment_field '{.spec.strategy.type}')"
expect_eq "rollingUpdate removed" "" "$(deployment_field '{.spec.strategy.rollingUpdate}')"
expect_eq "claim mounted" "$CLAIM" \
  "$(deployment_field '{.spec.template.spec.volumes[?(@.name=="vol-data")].persistentVolumeClaim.claimName}')"
expect_eq "claim kept on removal" "keep" \
  "$(kubectl get pvc "$CLAIM" -n "$NAMESPACE" -o jsonpath='{.metadata.annotations.helm\.sh/resource-policy}')"
expect_eq "claim access mode" "ReadWriteOnce" \
  "$(kubectl get pvc "$CLAIM" -n "$NAMESPACE" -o jsonpath='{.spec.accessModes[0]}')"
in_pod "echo '$MARKER' > /data/marker"
info "wrote $MARKER to /data/marker"

# ── 3. Redeploy ───────────────────────────────────────────────────────────────
step "Redeploying with a changed env var"
BEFORE=$(pods)
deploy "$(pond_yml 3 64Mi)"
AFTER=$(pods)
[[ -n "$AFTER" && "$AFTER" != "$BEFORE" ]] && info "ok: pod replaced ($BEFORE → $AFTER)" \
  || { warn "MISMATCH: expected a new pod, had '$BEFORE', now '$AFTER'"; FAILED=1; }
expect_eq "marker after redeploy" "$MARKER" "$(in_pod 'cat /data/marker')"

# ── 4. Remove the volume ──────────────────────────────────────────────────────
step "Removing the volume"
deploy "$(pond_yml 4)"
expect_eq "strategy after removal" "RollingUpdate" "$(deployment_field '{.spec.strategy.type}')"
expect_eq "pod volumes after removal" "" "$(deployment_field '{.spec.template.spec.volumes}')"
expect_eq "claim after removal" "Bound" \
  "$(kubectl get pvc "$CLAIM" -n "$NAMESPACE" -o jsonpath='{.status.phase}')"

# ── 5. Add it again ───────────────────────────────────────────────────────────
step "Adding the volume again"
deploy "$(pond_yml 5 64Mi)"
expect_eq "marker after re-adding" "$MARKER" "$(in_pod 'cat /data/marker')"

# ── 6. Shrink ─────────────────────────────────────────────────────────────────
step "Submitting a smaller volume"
if OUTPUT=$(submit "$(pond_yml 6 32Mi)"); then
  warn "MISMATCH: shrinking the volume was accepted: $OUTPUT"
  FAILED=1
elif grep -q 'never shrink' <<<"$OUTPUT"; then
  info "ok: rejected — $OUTPUT"
else
  warn "MISMATCH: rejected for another reason: $OUTPUT"
  FAILED=1
fi

[[ $FAILED -eq 0 ]] || die "one or more volume assertions failed"

step "Done — the volume kept its data through redeploys, removal and re-adding"

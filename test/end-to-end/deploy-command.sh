#!/usr/bin/env bash
# End-to-end scenario: command replaces the image's CMD and keeps its
# ENTRYPOINT, with templates rendered and every $ reaching the process as written.
#
# Flow:
#   1. Deploy with a command: the entrypoint runs it, {{service.port}} is
#      rendered, and the shell sees $$ rather than the $ Kubernetes would make of it
#   2. Remove the command: the image's CMD runs again
#   3. Submit a sensitive dependency output in the command: rejected
#   4. Submit an empty command: rejected rather than treated as unset
#
# Prerequisites:
#   - local-setup.sh has been run (creates test/end-to-end/.e2e-env)
#   - pond CLI binary built: make build-cli
#
# Usage:
#   ./test/end-to-end/deploy-command.sh

set -euo pipefail

SERVICE_NAME="command-demo"
NAMESPACE="${NAMESPACE:-e2e-staging}"
# nginx's entrypoint runs whatever command it is given, and the image has a shell.
COMMAND_IMAGE_TAG="${COMMAND_IMAGE_TAG:-alpine}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"
load_env

WORK_DIR=$(mktemp -d /tmp/pond-command.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT

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

# pond_yml <name> — writes stdin below the service's base fields and prints the path.
pond_yml() {
  local file="$WORK_DIR/$1.yml"
  {
    cat <<EOF
version: 1
name: $SERVICE_NAME
image: nginx
service:
  port: 80
EOF
    cat
  } >"$file"
  echo "$file"
}

submit() {
  POND_SERVER_URL="$SERVER_URL" POND_TOKEN="$POND_TOKEN" \
    "$POND" deploy \
      --pond-file "$1" \
      --project "$PROJECT_ID" \
      --env staging \
      --tag "$COMMAND_IMAGE_TAG" 2>&1
}

# deploy <pond_file> — submits and waits for the deployment to succeed
deploy() {
  local output id
  output=$(submit "$1") || die "submit failed: $output"
  echo "$output"
  id=$(get_deployment_id "$output")
  poll_status "$id" "succeeded" 150 2
}

# expect_rejected <what> <pond_file> <message> — the submit must fail with <message>
expect_rejected() {
  local what="$1" output
  if output=$(submit "$2"); then
    warn "MISMATCH: $what was accepted: $output"
    FAILED=1
  elif grep -qF "$3" <<<"$output"; then
    info "ok: $what rejected — $output"
  else
    warn "MISMATCH: $what rejected for another reason: $output"
    FAILED=1
  fi
}

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

in_pod() {
  kubectl exec -n "$NAMESPACE" "$(the_pod)" -- sh -c "$1"
}

container_field() {
  kubectl get deployment "$SERVICE_NAME" -n "$NAMESPACE" -o jsonpath="{.spec.template.spec.containers[0].$1}"
}

# ── 1. With a command ─────────────────────────────────────────────────────────
step "Deploying $SERVICE_NAME with a command"
deploy "$(pond_yml with-command <<'EOF'
command:
  - sh
  - -c
  - 'echo "port {{service.port}}, pid $$" > /tmp/started && exec nginx -g "daemon off;"'
EOF
)"
expect_eq "container command, so the image's entrypoint runs" "" "$(container_field command)"
# The entrypoint execs the command, so the shell is PID 1.
expect_eq "what the command wrote" "port 80, pid 1" "$(in_pod 'cat /tmp/started')"
expect_eq "nginx" "serving" "$(in_pod 'wget -qO /dev/null http://127.0.0.1/ && echo serving')"

# ── 2. Without it ─────────────────────────────────────────────────────────────
step "Removing the command"
deploy "$(pond_yml without-command </dev/null)"
expect_eq "container args" "" "$(container_field args)"
expect_eq "nothing written by the old command" "absent" "$(in_pod 'test -e /tmp/started && echo present || echo absent')"

# ── 3. A sensitive output ─────────────────────────────────────────────────────
step "Submitting a password in the command"
expect_rejected "a sensitive output" "$(pond_yml sensitive <<'EOF'
dependencies:
  db:
    type: postgres
command: [nginx, -g, "daemon off;", "{{db.password}}"]
EOF
)" "{{db.password}} is sensitive"

# ── 4. An empty command ───────────────────────────────────────────────────────
step "Submitting an empty command"
expect_rejected "an empty command" "$(pond_yml empty <<'EOF'
command: []
EOF
)" "must not be empty"

[[ $FAILED -eq 0 ]] || die "one or more command assertions failed"

step "Done — the command ran under the image's entrypoint, verbatim, and unsafe ones were rejected"

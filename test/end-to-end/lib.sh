#!/usr/bin/env bash
# Shared utilities for e2e scenario scripts.
# Source this file from scenario scripts; do not execute it directly.
#
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "$SCRIPT_DIR/lib.sh"
#   load_env

# Directory containing lib.sh (and .e2e-env, .e2e-pids, scenario scripts).
_E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

bold=$'\033[1m'; reset=$'\033[0m'; green=$'\033[32m'; yellow=$'\033[33m'; red=$'\033[31m'
step()  { echo; echo "${bold}${green}▶ $*${reset}"; }
info()  { echo "  ${yellow}$*${reset}"; }
warn()  { echo "  ${yellow}⚠ $*${reset}"; }
die()   { echo "${bold}${red}✖ ERROR: $*${reset}" >&2; exit 1; }

# load_env — sources .e2e-env if POND_TOKEN is not already set, then validates
# and exports SERVER_URL, POND_TOKEN, PROJECT_ID, IMAGE_TAG, and POND (CLI path).
load_env() {
  local env_file="$_E2E_DIR/.e2e-env"
  if [[ -f "$env_file" && -z "${POND_TOKEN:-}" ]]; then
    source "$env_file"
  fi

  SERVER_URL="${POND_SERVER_URL:-http://localhost:8080}"
  POND_TOKEN="${POND_TOKEN:?POND_TOKEN must be set — run local-setup.sh first}"
  PROJECT_ID="${POND_PROJECT_ID:?POND_PROJECT_ID must be set — run local-setup.sh first}"
  IMAGE_TAG="${POND_IMAGE_TAG:-latest}"
  POND="$_E2E_DIR/../../build/pond"
  [[ -x "$POND" ]] || die "CLI binary not found at $POND — run: make build-cli"
}

# poll_status <deployment_id> <expected_status> [max_tries=60] [interval=2]
#
# Polls GET /api/v1/deployments/<id> every <interval> seconds.
# Returns 0 when status matches <expected_status>.
# Dies immediately if deployment reaches a terminal status that is not <expected>.
# Dies after <max_tries> attempts if the expected status is never reached.
poll_status() {
  local id="$1" expected="$2" max_tries="${3:-60}" interval="${4:-2}"
  local i status

  for i in $(seq 1 "$max_tries"); do
    status=$(curl -sf \
      -H "Authorization: Bearer $POND_TOKEN" \
      "$SERVER_URL/api/v1/deployments/$id" \
      | jq -r '.status') \
      || die "Could not reach server while polling deployment $id"

    info "[$i/$max_tries] status: $status"

    if [[ "$status" == "$expected" ]]; then
      return 0
    fi

    if [[ "$status" == "failed" || "$status" == "succeeded" ]]; then
      die "Deployment reached terminal status '$status' before '$expected'"
    fi

    sleep "$interval"
  done

  die "Timed out waiting for status '$expected' (last: ${status:-unknown})"
}

# get_deployment_id <cli_output>
# Extracts the deployment UUID from CLI output.
# The CLI prints the deployment ID as a UUID when submitting without --wait.
get_deployment_id() {
  local output="$1"
  local id
  id=$(echo "$output" \
    | grep -oE '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}' \
    | head -1)
  [[ -n "$id" ]] || die "Could not parse a deployment ID from CLI output"
  echo "$id"
}

#!/usr/bin/env bash
# Upgrades the pond Helm releases of a cluster (pond-agent, then pond-server) to a
# given version, keeping each release's configured values (secrets, agent token,
# image repository, ...). Releases not installed in the cluster are skipped.
#
#   scripts/upgrade.sh 0.3.0-alpha3 --kube-context mhard
#
# Needs: helm 3.14+, kubectl, jq.

set -euo pipefail

CHARTS="oci://ghcr.io/pondplatform/charts"
# Agents first: an agent older than the server can't run the commands it sends.
RELEASES=(pond-agent pond-server)
NAMESPACE="pond"
KUBE_CONTEXT=""
VERSION=""
ASSUME_YES="false"

usage() {
  cat <<EOF
Usage: upgrade.sh <version> --kube-context <ctx> [OPTIONS]

Upgrades the ${RELEASES[*]} releases in the cluster to <version>
(e.g. 0.3.0-alpha3 or v0.3.0-alpha3): charts from ${CHARTS} at that version,
image tag v<version>. Agents are upgraded before the server; a release that
isn't installed in the cluster is skipped, so run this once per cluster that
runs an agent. Each release's existing values are kept; new chart defaults are
picked up.

Options:
  --kube-context <ctx>   kubectl context of the cluster (required)
  --namespace <ns>       namespace of the releases (default: ${NAMESPACE})
  -y, --yes              don't ask for confirmation
  -h, --help             show this help
EOF
}

die() { echo "ERROR: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --kube-context) [[ $# -ge 2 ]] || die "--kube-context needs a value"; KUBE_CONTEXT="$2"; shift 2 ;;
    --namespace)    [[ $# -ge 2 ]] || die "--namespace needs a value"; NAMESPACE="$2"; shift 2 ;;
    -y|--yes)       ASSUME_YES="true"; shift ;;
    -h|--help)      usage; exit 0 ;;
    -*)             usage >&2; die "unknown option: $1" ;;
    *)              [[ -z "$VERSION" ]] || die "unexpected argument: $1"; VERSION="$1"; shift ;;
  esac
done

[[ -n "$VERSION" ]]      || { usage >&2; die "missing <version>"; }
[[ -n "$KUBE_CONTEXT" ]] || { usage >&2; die "missing --kube-context"; }
for cmd in helm kubectl jq; do
  command -v "$cmd" >/dev/null || die "$cmd not found"
done

CHART_VERSION="${VERSION#v}"
IMAGE_TAG="v${CHART_VERSION}"
HELM=(helm --kube-context "$KUBE_CONTEXT" --namespace "$NAMESPACE")

echo "Context: $KUBE_CONTEXT (namespace $NAMESPACE)"
installed=0
pending=()
for release in "${RELEASES[@]}"; do
  if ! metadata=$("${HELM[@]}" get metadata "$release" -o json 2>/dev/null); then
    printf '  %-12s not installed, skipped\n' "$release"
    continue
  fi
  installed=$((installed + 1))
  current_chart=$(jq -r '.version' <<<"$metadata")
  current_tag=$("${HELM[@]}" get values "$release" --all -o json | jq -r '.image.tag // "?"')
  if [[ "$current_chart" == "$CHART_VERSION" && "$current_tag" == "$IMAGE_TAG" ]]; then
    printf '  %-12s already at %s\n' "$release" "$CHART_VERSION"
    continue
  fi
  helm show chart "$CHARTS/$release" --version "$CHART_VERSION" >/dev/null 2>&1 \
    || die "chart $CHARTS/$release version $CHART_VERSION not found"
  printf '  %-12s chart %s -> %s, image tag %s -> %s\n' \
    "$release" "$current_chart" "$CHART_VERSION" "$current_tag" "$IMAGE_TAG"
  pending+=("$release")
done

(( installed > 0 )) \
  || die "no ${RELEASES[*]} release in namespace '$NAMESPACE' of context '$KUBE_CONTEXT'"
if (( ${#pending[@]} == 0 )); then
  echo "Nothing to upgrade."
  exit 0
fi

if [[ "$ASSUME_YES" != "true" ]]; then
  read -r -p "Upgrade ${pending[*]}? [y/N] " answer || answer=""
  [[ "$answer" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
fi

for release in "${pending[@]}"; do
  echo "Upgrading $release ..."
  # --reset-then-reuse-values, unlike --reuse-values, also applies defaults added in the new chart.
  "${HELM[@]}" upgrade "$release" "$CHARTS/$release" \
    --version "$CHART_VERSION" \
    --reset-then-reuse-values \
    --set image.tag="$IMAGE_TAG" \
    --wait --timeout 5m \
    || die "$release upgrade failed. Logs: kubectl --context $KUBE_CONTEXT -n $NAMESPACE logs deploy/$release
       Roll back: helm --kube-context $KUBE_CONTEXT -n $NAMESPACE rollback $release"

  image=$(kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get deploy "$release" \
    -o jsonpath='{.spec.template.spec.containers[0].image}')
  echo "Upgraded $release to $CHART_VERSION (running $image)."
done

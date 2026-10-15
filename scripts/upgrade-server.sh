#!/usr/bin/env bash
# Upgrades an existing pond-server Helm release to a given version, keeping the
# release's configured values (secrets, image repository, ...).
#
#   scripts/upgrade-server.sh 0.3.0-alpha2 --kube-context mhard
#
# Needs: helm 3.14+, kubectl, jq.

set -euo pipefail

CHART="oci://ghcr.io/pondplatform/charts/pond-server"
RELEASE="pond-server"
NAMESPACE="pond"
KUBE_CONTEXT=""
VERSION=""
ASSUME_YES="false"

usage() {
  cat <<EOF
Usage: upgrade-server.sh <version> --kube-context <ctx> [OPTIONS]

Upgrades the pond-server release to <version> (e.g. 0.3.0-alpha2 or v0.3.0-alpha2):
chart ${CHART} at that version, image tag v<version>.
The release's existing values are kept; new chart defaults are picked up.

Options:
  --kube-context <ctx>   kubectl context of the cluster running pond-server (required)
  --namespace <ns>       namespace of the release (default: ${NAMESPACE})
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

metadata=$("${HELM[@]}" get metadata "$RELEASE" -o json 2>/dev/null) \
  || die "no '$RELEASE' release in namespace '$NAMESPACE' of context '$KUBE_CONTEXT'"
current_chart=$(jq -r '.version' <<<"$metadata")
current_tag=$("${HELM[@]}" get values "$RELEASE" --all -o json | jq -r '.image.tag // "?"')

if [[ "$current_chart" == "$CHART_VERSION" && "$current_tag" == "$IMAGE_TAG" ]]; then
  echo "$RELEASE in '$KUBE_CONTEXT' is already at $CHART_VERSION."
  exit 0
fi

helm show chart "$CHART" --version "$CHART_VERSION" >/dev/null 2>&1 \
  || die "chart $CHART version $CHART_VERSION not found"

cat <<EOF
Context:   $KUBE_CONTEXT (namespace $NAMESPACE, release $RELEASE)
Chart:     $current_chart -> $CHART_VERSION
Image tag: $current_tag -> $IMAGE_TAG
EOF

if [[ "$ASSUME_YES" != "true" ]]; then
  read -r -p "Upgrade? [y/N] " answer || answer=""
  [[ "$answer" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
fi

# --reset-then-reuse-values, unlike --reuse-values, also applies defaults added in the new chart.
"${HELM[@]}" upgrade "$RELEASE" "$CHART" \
  --version "$CHART_VERSION" \
  --reset-then-reuse-values \
  --set image.tag="$IMAGE_TAG" \
  --wait --timeout 5m \
  || die "upgrade failed. Logs: kubectl --context $KUBE_CONTEXT -n $NAMESPACE logs deploy/$RELEASE
       Roll back: helm --kube-context $KUBE_CONTEXT -n $NAMESPACE rollback $RELEASE"

image=$(kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get deploy "$RELEASE" \
  -o jsonpath='{.spec.template.spec.containers[0].image}')
echo "Upgraded $RELEASE to $CHART_VERSION (running $image)."

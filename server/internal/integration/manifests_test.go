//go:build integration

package integration

import (
	"context"
	"testing"
	"time"
)

// TestManifests_GatewayRoute checks the HTTPRoute the gateway fixture becomes:
// a hostname derived from the environment's base host, the platform's
// parentRefs and backendRefs, and the user's matches and filters as written.
func TestManifests_GatewayRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenarioWithBaseHost(ctx, t, h, "apps.test")
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	objs := deploySucceeds(ctx, t, h, fa, SubmitFileRequest(t, scenario, "gateway/pond.yml"))

	expectField(t, objs, "HTTPRoute/gateway-demo", "spec", `
parentRefs:
  - {name: gateway, sectionName: http}
hostnames: [gateway-demo.apps.test]
rules:
  - matches: [{path: {type: PathPrefix, value: /rewritten}}]
    filters:
      - type: URLRewrite
        urlRewrite: {path: {type: ReplacePrefixMatch, replacePrefixMatch: /}}
      - type: RequestHeaderModifier
        requestHeaderModifier: {set: [{name: X-Pond-Filter, value: applied}]}
    backendRefs: [{name: gateway-demo, port: 80, weight: 1}]
  - matches: [{path: {type: PathPrefix, value: /}}]
    backendRefs: [{name: gateway-demo, port: 80, weight: 1}]
`)
	expectField(t, objs, "Deployment/gateway-demo", "spec.replicas", `1`)
}

// TestManifests_VolumeAddedAndRemoved checks a volume's claim and mount, and
// that removing it switches the rollout back while the claim is left to
// helm's keep policy.
func TestManifests_VolumeAddedAndRemoved(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	objs := deploySucceeds(ctx, t, h, fa, SubmitFileRequest(t, scenario, "volumes/1-with-volume.yml"))
	// A ReadWriteOnce claim can't be mounted by the old and the new pod at once.
	expectField(t, objs, "Deployment/volumes-demo", "spec.strategy", `{type: Recreate}`)
	expectField(t, objs, "Deployment/volumes-demo", "spec.template.spec.volumes",
		`[{name: vol-data, persistentVolumeClaim: {claimName: volumes-demo-data}}]`)
	expectField(t, objs, "Deployment/volumes-demo", "spec.template.spec.containers.0.volumeMounts",
		`[{name: vol-data, mountPath: /data}]`)
	expectField(t, objs, "PersistentVolumeClaim/volumes-demo-data", "metadata.annotations",
		`{helm.sh/resource-policy: keep}`)
	expectField(t, objs, "PersistentVolumeClaim/volumes-demo-data", "spec",
		`{accessModes: [ReadWriteOnce], resources: {requests: {storage: 64Mi}}}`)

	removed := SubmitFileRequest(t, scenario, "volumes/1-with-volume.yml")
	removed.OverridableConfig.Volumes = nil
	objs = deploySucceeds(ctx, t, h, fa, removed)
	expectField(t, objs, "Deployment/volumes-demo", "spec.strategy",
		`{type: RollingUpdate, rollingUpdate: {maxSurge: 25%, maxUnavailable: 25%}}`)
	expectField(t, objs, "Deployment/volumes-demo", "spec.template.spec.volumes", `null`)
	expectField(t, objs, "Deployment/volumes-demo", "spec.template.spec.containers.0.volumeMounts", `null`)
	if objs.Has("PersistentVolumeClaim/volumes-demo-data") {
		t.Error("the removed volume's claim is still rendered")
	}
}

// TestManifests_Command checks a command becomes the container's args, so the
// image's entrypoint still runs, with templates rendered and every $ doubled
// so Kubernetes hands it to the process as written; and that removing the
// command removes the args.
func TestManifests_Command(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenario(ctx, t, h)
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	objs := deploySucceeds(ctx, t, h, fa, SubmitFileRequest(t, scenario, "command/pond.yml"))
	expectField(t, objs, "Deployment/command-demo", "spec.template.spec.containers.0.command", `null`)
	expectField(t, objs, "Deployment/command-demo", "spec.template.spec.containers.0.args", yamlOf(t, []string{
		"sh", "-c", `echo "port 80, pid $$$$" > /tmp/started && exec nginx -g "daemon off;"`,
	}))

	without := SubmitFileRequest(t, scenario, "command/pond.yml")
	without.OverridableConfig.Command = nil
	objs = deploySucceeds(ctx, t, h, fa, without)
	expectField(t, objs, "Deployment/command-demo", "spec.template.spec.containers.0.args", `null`)
}

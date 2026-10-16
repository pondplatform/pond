//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/shared/agent/wire"
	shared "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

// TestServiceDependency_ImportsTheProvidersAddresses deploys the provider, then
// the consumer, and checks the consumer's env holds the provider's addresses
// as its rollout published them, through a plain-http gateway on port 8000,
// and its public variables, secret or not, but never a private one.
func TestServiceDependency_ImportsTheProvidersAddresses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenarioWithBaseHost(ctx, t, h, "apps.test")
	patchEnvironment(ctx, t, h, scenario.EnvID, map[string]any{"gatewayPort": 8000, "gatewayTls": false})
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	providerReq := SubmitFileRequest(t, scenario, "service-dependency/1-provider.yml")
	providerReq.OverridableConfig.Variables = map[string]serviceconfig.VariableDeclaration{
		"API_KEY": {Secret: true, Visibility: "public"},
		"REGION":  {Visibility: "public"},
		"OWN":     {},
	}
	providerReq.Variables = map[string]string{"API_KEY": "k3y", "REGION": "eu", "OWN": "mine"}
	provider := submitDeployment(ctx, t, h, providerReq)
	if body := waitForTerminal(ctx, t, h, provider, 10*time.Second); field(body, "status") != "succeeded" {
		t.Fatalf("expected the provider to succeed, got %q (error %q)", field(body, "status"), field(body, "error"))
	}
	consumerReq := SubmitFileRequest(t, scenario, "service-dependency/2-consumer.yml")
	consumerReq.OverridableConfig.Env["PROVIDER_API_KEY"] = "{{dependencies.provider.variables.API_KEY}}"
	consumerReq.OverridableConfig.Env["PROVIDER_REGION"] = "{{dependencies.provider.variables.REGION}}"
	private := consumerReq
	private.OverridableConfig.Env = maps.Clone(consumerReq.OverridableConfig.Env)
	private.OverridableConfig.Env["PROVIDER_OWN"] = "{{dependencies.provider.variables.OWN}}"
	if _, err := h.Client().SubmitDeployment(ctx, private); err == nil || !strings.Contains(err.Error(), "has no public variable") {
		t.Fatalf("expected the private variable to be refused, got %v", err)
	}
	consumer := submitDeployment(ctx, t, h, consumerReq)
	if body := waitForTerminal(ctx, t, h, consumer, 10*time.Second); field(body, "status") != "succeeded" {
		t.Fatalf("expected the consumer to succeed, got %q (error %q)\n%s", field(body, "status"), field(body, "error"), describeDeployment(h.DB, consumer))
	}

	want := map[string]string{
		"PROVIDER_HOST":        "dep-provider.test-namespace.svc.cluster.local",
		"PROVIDER_PORT":        "8080",
		"PROVIDER_URL":         "http://dep-provider.test-namespace.svc.cluster.local:8080",
		"PROVIDER_PUBLIC_HOST": "dep-provider.apps.test",
		"PROVIDER_PUBLIC_URL":  "http://dep-provider.apps.test:8000",
		"PROVIDER_API_KEY":     "k3y",
		"PROVIDER_REGION":      "eu",
	}
	if got := helmValuesOf(t, fa, consumer).Env; !maps.Equal(got, want) {
		t.Errorf("expected env %v, got %v", want, got)
	}
}

// TestServiceDependency_Rejected checks the submits that can't import: the
// provider isn't deployed yet, or the consumer wants a gateway the provider
// doesn't have. Neither writes a deployment of the consumer.
func TestServiceDependency_Rejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewTestHarness(t, testConnStr)
	scenario := BuildScenarioWithBaseHost(ctx, t, h, "apps.test")
	fa := h.StartFakeAgent(ctx, t, scenario.AgentToken, DefaultBehavior)

	expectRejected := func(want string) {
		t.Helper()
		_, err := h.Client().SubmitDeployment(ctx, SubmitFileRequest(t, scenario, "service-dependency/2-consumer.yml"))
		if err == nil || !strings.Contains(err.Error(), "status 422") || !strings.Contains(err.Error(), want) {
			t.Fatalf("expected a 422 containing %q, got %v", want, err)
		}
	}

	expectRejected(`not found in the project`)

	withoutGateway := SubmitFileRequest(t, scenario, "service-dependency/1-provider.yml")
	withoutGateway.OverridableConfig.Gateway = nil
	deploySucceeds(ctx, t, h, fa, withoutGateway)
	expectRejected(`gateway values need a gateway route`)

	if n := countDeployments(t, h, scenario.ProjectID); n != 1 {
		t.Errorf("expected only the provider's deployment, got %d", n)
	}
}

func patchEnvironment(ctx context.Context, t *testing.T, h *TestHarness, envID uuid.UUID, body map[string]any) {
	t.Helper()
	resp, err := newScenarioClient(h.BaseURL, h.AdminToken).do(ctx, http.MethodPatch, fmt.Sprintf("/api/v1/environments/%s", envID), body)
	if err != nil {
		t.Fatalf("patch environment: %v", err)
	}
	defer resp.Body.Close()
	var env shared.Environment
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("patch environment: status %d, %v", resp.StatusCode, err)
	}
	if port, ok := body["gatewayPort"].(int); ok && (env.GatewayPort == nil || int(*env.GatewayPort) != port) {
		t.Errorf("expected gateway port %d, got %v", port, env.GatewayPort)
	}
	if tls, ok := body["gatewayTls"].(bool); ok && env.GatewayTLS != tls {
		t.Errorf("expected gateway tls %v, got %v", tls, env.GatewayTLS)
	}
}

// helmValuesOf decodes the values of the deployment's helm.upgrade.
func helmValuesOf(t *testing.T, fa *FakeAgent, deploymentID uuid.UUID) helmgen.HelmValues {
	t.Helper()
	for _, cmd := range fa.ReceivedCommands() {
		if cmd.Type != wire.CommandHelmUpgrade || cmd.DeploymentID != deploymentID {
			continue
		}
		var payload wire.HelmUpgradePayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			t.Fatalf("decode helm.upgrade payload: %v", err)
		}
		var values helmgen.HelmValues
		if err := yaml.Unmarshal(payload.Values, &values); err != nil {
			t.Fatalf("decode helm values: %v", err)
		}
		return values
	}
	t.Fatalf("the agent received no helm.upgrade for deployment %s", deploymentID)
	return helmgen.HelmValues{}
}

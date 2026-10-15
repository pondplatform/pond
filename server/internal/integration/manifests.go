//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/agent/wire"
	shared "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"gopkg.in/yaml.v3"
)

// testDataDir holds the pond.yml files the end-to-end scenarios deploy, so
// both suites exercise the same cases.
const testDataDir = "../../../test/test-data"

// baseServiceChart is the repo copy of the chart the agent image ships.
const baseServiceChart = "../../../infra/agent/helm-charts/base-service"

// SubmitFileRequest builds the request `pond deploy` sends for a pond.yml
// under test/test-data.
func SubmitFileRequest(t *testing.T, scenario ScenarioResult, name string) shared.SubmitRequest {
	t.Helper()
	cfg, err := config.NewParser().ParseFile(filepath.Join(testDataDir, name))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	req := SubmitRequest(scenario, serviceconfig.ServiceConfig{}, "v1.0.0")
	req.OverridableConfig = *cfg
	return req
}

// Manifests are the objects a helm.upgrade applies, keyed by "Kind/name".
type Manifests map[string]map[string]any

// deploySucceeds submits req, waits for the deployment to succeed and returns
// the objects its helm.upgrade applies.
func deploySucceeds(ctx context.Context, t *testing.T, h *TestHarness, fa *FakeAgent, req shared.SubmitRequest) Manifests {
	t.Helper()
	id := submitDeployment(ctx, t, h, req)
	if body := waitForTerminal(ctx, t, h, id, 10*time.Second); field(body, "status") != "succeeded" {
		t.Fatalf("expected deployment %s to succeed, got %q (error %q)\n%s", id, field(body, "status"), field(body, "error"), describeDeployment(h.DB, id))
	}
	return renderHelmUpgrade(t, fa, id)
}

// renderHelmUpgrade renders the deployment's helm.upgrade with the chart, as
// the agent would install it.
func renderHelmUpgrade(t *testing.T, fa *FakeAgent, deploymentID uuid.UUID) Manifests {
	t.Helper()
	var payload *wire.HelmUpgradePayload
	for _, cmd := range fa.ReceivedCommands() {
		if cmd.Type == wire.CommandHelmUpgrade && cmd.DeploymentID == deploymentID {
			payload = &wire.HelmUpgradePayload{}
			if err := json.Unmarshal(cmd.Payload, payload); err != nil {
				t.Fatalf("decode helm.upgrade payload: %v", err)
			}
		}
	}
	if payload == nil {
		t.Fatalf("the agent received no helm.upgrade for deployment %s", deploymentID)
	}
	if filepath.Base(payload.ChartPath) != filepath.Base(baseServiceChart) {
		t.Fatalf("the agent would install %s, but only %s can be rendered here", payload.ChartPath, baseServiceChart)
	}

	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, payload.Values, 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("helm", "template", payload.ReleaseName, baseServiceChart,
		"--namespace", payload.Namespace, "--values", values)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template (helm must be on PATH): %v\n%s\nvalues:\n%s", err, stderr.String(), payload.Values)
	}

	objs := Manifests{}
	dec := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode rendered manifests: %v\n%s", err, out)
		}
		if doc == nil {
			continue
		}
		kind, _ := doc["kind"].(string)
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		objs[kind+"/"+name] = doc
	}
	return objs
}

// Has reports whether the object was rendered.
func (m Manifests) Has(object string) bool {
	_, ok := m[object]
	return ok
}

// field returns the value at a dotted path such as
// spec.template.spec.containers.0.args, or nil where the path ends early.
func (m Manifests) field(t *testing.T, object, path string) any {
	t.Helper()
	obj, ok := m[object]
	if !ok {
		t.Fatalf("%s was not rendered; got %v", object, slices.Sorted(maps.Keys(m)))
	}
	var cur any = obj
	for _, key := range strings.Split(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			cur = v[i]
		default:
			return nil
		}
	}
	return cur
}

// expectField fails the test unless the object's field at path equals want,
// written as YAML; "null" expects the field to be absent.
func expectField(t *testing.T, m Manifests, object, path, want string) {
	t.Helper()
	var w any
	if err := yaml.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("expectation for %s %s is not YAML: %v", object, path, err)
	}
	if got := m.field(t, object, path); !reflect.DeepEqual(got, w) {
		gotYAML, _ := yaml.Marshal(got)
		wantYAML, _ := yaml.Marshal(w)
		t.Errorf("%s %s\nwant:\n%s\ngot:\n%s", object, path, wantYAML, gotYAML)
	}
}

// yamlOf writes v as YAML for expectField, for values awkward to quote by hand.
func yamlOf(t *testing.T, v any) string {
	t.Helper()
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(out)
}

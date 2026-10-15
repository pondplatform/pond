package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	pondconfig "github.com/pondplatform/pond/cli/internal/config"
	api "github.com/pondplatform/pond/shared/server/api"
)

// With no config file there is no context to fall back to, so an ignored
// variable shows up as a request to the wrong server, project or environment,
// or one without the token. This is the path that regressed when the CLI
// ignored POND_PROJECT_ID.
func TestRootCmdTakesItsConfigurationFromTheEnvironment(t *testing.T) {
	projectID, serviceID := uuid.New(), uuid.New()
	stagingID, productionID := uuid.New(), uuid.New()
	deploymentID := uuid.New()

	items := func(w http.ResponseWriter, v any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": v})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer env-token" {
			t.Errorf("%s %s: expected the token from %s, got Authorization %q", r.Method, r.URL, pondconfig.EnvToken, got)
		}
		switch r.URL.Path {
		case "/api/v1/projects/" + projectID.String() + "/services":
			items(w, []api.Service{{ID: serviceID, ProjectID: projectID, Name: "postgres-demo"}})
		case "/api/v1/projects/" + projectID.String() + "/environments":
			items(w, []api.Environment{{ID: stagingID, Name: "staging"}, {ID: productionID, Name: "production"}})
		case "/api/v1/services/" + serviceID.String() + "/deployments":
			if got := r.URL.Query().Get("environment_id"); got != stagingID.String() {
				t.Errorf("expected the listing scoped to %s from %s, got environment_id %q", "staging", pondconfig.EnvEnvironment, got)
			}
			items(w, []api.DeploymentListItem{{
				ID: deploymentID, ServiceID: serviceID, EnvironmentID: stagingID,
				Status: api.DeploymentStatusSucceeded, ImageTag: "v1", TriggeredBy: "cli", CreatedAt: time.Now(),
			}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv(pondconfig.EnvServerURL, srv.URL)
	t.Setenv(pondconfig.EnvToken, "env-token")
	t.Setenv(pondconfig.EnvProjectID, projectID.String())
	t.Setenv(pondconfig.EnvEnvironment, "staging")
	t.Setenv(pondconfig.EnvConfig, filepath.Join(t.TempDir(), "never-created"))
	// Blank, so the developer's own shell can't steer the resolution or start a port-forward.
	for _, name := range []string{pondconfig.EnvProject, pondconfig.EnvContext, pondconfig.EnvKubeContext,
		pondconfig.EnvKubeNamespace, pondconfig.EnvKubeService, pondconfig.EnvForwardPort} {
		t.Setenv(name, "")
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"deployment", "list", "--service", "postgres-demo"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("deployment list: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), deploymentID.String()) {
		t.Errorf("expected deployment %s in the listing, got:\n%s", deploymentID, out.String())
	}
}

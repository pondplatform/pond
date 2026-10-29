package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// init only writes local files, so it takes the environment from the usual
// chain without starting the port-forward a context or the environment
// configures. With no kubectl on PATH, starting one would print a warning.
func TestInitResolvesTheEnvironmentWithoutStartingAPortForward(t *testing.T) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	freePort := l.Addr().(*net.TCPAddr).Port
	l.Close()

	t.Setenv(pondconfig.EnvEnvironment, "staging")
	t.Setenv(pondconfig.EnvConfig, filepath.Join(t.TempDir(), "never-created"))
	t.Setenv(pondconfig.EnvContext, "")
	t.Setenv(pondconfig.EnvKubeContext, "pond")
	t.Setenv(pondconfig.EnvKubeNamespace, "pond")
	t.Setenv(pondconfig.EnvKubeService, "")
	t.Setenv(pondconfig.EnvForwardPort, strconv.Itoa(freePort))
	t.Setenv("PATH", t.TempDir())

	dir := t.TempDir()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "--image", "ghcr.io/acme/api", "-f", filepath.Join(dir, "pond.yml")})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "pond-input-staging.yml")); err != nil {
		t.Errorf("expected the input file for the environment from %s: %v", pondconfig.EnvEnvironment, err)
	}
	if strings.Contains(out.String(), "port-forward") {
		t.Errorf("expected no port-forward to be started, got:\n%s", out.String())
	}
}

// The --env flag lives on the root, so only a run through it can show that
// service delete refuses the flag before talking to the server.
func TestServiceDeleteRefusesAnEnvironmentFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	t.Setenv(pondconfig.EnvServerURL, srv.URL)
	t.Setenv(pondconfig.EnvProjectID, uuid.New().String())
	t.Setenv(pondconfig.EnvConfig, filepath.Join(t.TempDir(), "never-created"))
	for _, name := range []string{pondconfig.EnvProject, pondconfig.EnvEnvironment, pondconfig.EnvContext, pondconfig.EnvKubeContext,
		pondconfig.EnvKubeNamespace, pondconfig.EnvKubeService, pondconfig.EnvForwardPort} {
		t.Setenv(name, "")
	}

	for _, flag := range []string{"--env", "-e"} {
		t.Run(flag, func(t *testing.T) {
			root := NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{"service", "delete", "api", "--yes", flag, "staging"})
			err := root.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "takes no environment") {
				t.Fatalf("expected %s to be refused, got %v\n%s", flag, err, out.String())
			}
		})
	}
}

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pondplatform/pond/cli/internal/config"
)

// clearPondEnv neutralizes every variable the CLI reads, so the developer's own
// environment cannot influence a resolution test.
func clearPondEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		config.EnvServerURL, config.EnvToken, config.EnvProject, config.EnvProjectID,
		config.EnvEnvironment, config.EnvContext, config.EnvConfig,
		config.EnvKubeContext, config.EnvKubeNamespace, config.EnvKubeService,
		config.EnvForwardPort,
	} {
		t.Setenv(name, "")
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if cfg.CurrentContext != "" {
		t.Errorf("expected empty current context, got %q", cfg.CurrentContext)
	}
	if len(cfg.Contexts) != 0 {
		t.Errorf("expected no contexts, got %v", cfg.Contexts)
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pond", "config")
	orig := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {Server: "https://prod.example.com", Token: "tok", Project: "proj-uuid", Env: "production"},
		},
	}
	if err := config.Save(path, orig); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.CurrentContext != orig.CurrentContext {
		t.Errorf("current-context: got %q, want %q", got.CurrentContext, orig.CurrentContext)
	}
	c := got.Contexts["prod"]
	if c.Server != "https://prod.example.com" || c.Token != "tok" || c.Project != "proj-uuid" || c.Env != "production" {
		t.Errorf("unexpected context value: %+v", c)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".pond", "config")
	if err := config.Save(path, &config.Config{Contexts: map[string]config.Context{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("dir mode: got %v, want 0700", info.Mode().Perm())
	}
}

func TestAtomicWriteNoTmpSibling(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pond", "config")
	if err := config.Save(path, &config.Config{Contexts: map[string]config.Context{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after save")
	}
}

func TestResolveFlags(t *testing.T) {
	clearPondEnv(t)
	t.Setenv("POND_SERVER_URL", "http://env-server")
	t.Setenv("POND_TOKEN", "env-token")
	t.Setenv("POND_PROJECT", "env-proj")
	t.Setenv("POND_ENV", "env-env")

	cfg := &config.Config{
		CurrentContext: "ctx",
		Contexts: map[string]config.Context{
			"ctx": {Server: "http://ctx-server", Token: "ctx-token", Project: "ctx-proj", Env: "ctx-env"},
		},
	}
	flags := config.Flags{Server: "http://flag-server", Token: "flag-token", Project: "flag-proj", Env: "flag-env"}
	resolved, err := config.Resolve(flags, cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://flag-server" {
		t.Errorf("server: got %q, want flag value", resolved.Server)
	}
	if resolved.Token != "flag-token" {
		t.Errorf("token: got %q, want flag value", resolved.Token)
	}
	if resolved.Project != "flag-proj" {
		t.Errorf("project: got %q, want flag value", resolved.Project)
	}
	if resolved.Env != "flag-env" {
		t.Errorf("env: got %q, want flag value", resolved.Env)
	}
}

func TestResolveEnvVars(t *testing.T) {
	clearPondEnv(t)
	t.Setenv("POND_SERVER_URL", "http://env-server")
	t.Setenv("POND_TOKEN", "env-token")
	t.Setenv("POND_PROJECT", "env-proj")
	t.Setenv("POND_ENV", "env-env")

	cfg := &config.Config{
		CurrentContext: "ctx",
		Contexts: map[string]config.Context{
			"ctx": {Server: "http://ctx-server", Token: "ctx-token", Project: "ctx-proj", Env: "ctx-env"},
		},
	}
	resolved, err := config.Resolve(config.Flags{}, cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://env-server" {
		t.Errorf("server: got %q, want env value", resolved.Server)
	}
	if resolved.Project != "env-proj" {
		t.Errorf("project: got %q, want env value", resolved.Project)
	}
	if resolved.Token != "env-token" {
		t.Errorf("token: got %q, want env value", resolved.Token)
	}
	if resolved.Env != "env-env" {
		t.Errorf("env: got %q, want env value", resolved.Env)
	}
}

func TestResolveContext(t *testing.T) {
	clearPondEnv(t)
	cfg := &config.Config{
		CurrentContext: "staging",
		Contexts: map[string]config.Context{
			"staging": {Server: "http://staging", Token: "stg-tok", Project: "stg-proj", Env: "staging"},
		},
	}
	resolved, err := config.Resolve(config.Flags{}, cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://staging" {
		t.Errorf("server: got %q", resolved.Server)
	}
	if resolved.Project != "stg-proj" {
		t.Errorf("project: got %q", resolved.Project)
	}
}

func TestResolveDefaults(t *testing.T) {
	clearPondEnv(t)
	cfg := &config.Config{Contexts: map[string]config.Context{}}
	resolved, err := config.Resolve(config.Flags{}, cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://localhost:8080" {
		t.Errorf("server default: got %q", resolved.Server)
	}
	if resolved.Token != "" || resolved.Project != "" || resolved.Env != "" {
		t.Errorf("expected empty token/project/env, got %+v", resolved)
	}
}

func TestResolveUnknownContext(t *testing.T) {
	clearPondEnv(t)
	cfg := &config.Config{Contexts: map[string]config.Context{}}
	_, err := config.Resolve(config.Flags{Context: "nonexistent"}, cfg)
	if err == nil {
		t.Error("expected error for unknown context")
	}
}

func TestResolveNamedContextFlagOverride(t *testing.T) {
	clearPondEnv(t)
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Server: "http://prod"},
			"staging": {Server: "http://staging"},
		},
	}
	resolved, err := config.Resolve(config.Flags{Context: "staging"}, cfg)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://staging" {
		t.Errorf("expected staging server, got %q", resolved.Server)
	}
}

// projectCfg is the fixture for the project precedence tests below.
func projectCfg() *config.Config {
	return &config.Config{
		CurrentContext: "ctx",
		Contexts: map[string]config.Context{
			"ctx": {Server: "http://ctx-server", Project: "ctx-proj"},
		},
	}
}

// POND_PROJECT_ID is what the e2e env file and scripts/setup.sh export. Before it was
// honoured the context's stale project won, which produced a confusing 404.
func TestResolveProjectIDEnvFallback(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvProjectID, "env-proj-id")

	resolved, err := config.Resolve(config.Flags{}, projectCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Project != "env-proj-id" {
		t.Errorf("project: got %q, want POND_PROJECT_ID value", resolved.Project)
	}
}

func TestResolveProjectEnvPrecedence(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvProject, "env-proj")
	t.Setenv(config.EnvProjectID, "env-proj-id")

	resolved, err := config.Resolve(config.Flags{}, projectCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Project != "env-proj" {
		t.Errorf("project: got %q, want POND_PROJECT to win", resolved.Project)
	}
}

func TestResolveProjectFlagBeatsBothEnvNames(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvProject, "env-proj")
	t.Setenv(config.EnvProjectID, "env-proj-id")

	resolved, err := config.Resolve(config.Flags{Project: "flag-proj"}, projectCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Project != "flag-proj" {
		t.Errorf("project: got %q, want flag value", resolved.Project)
	}
}

func twoContextCfg() *config.Config {
	return &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Server: "http://prod"},
			"staging": {Server: "http://staging"},
		},
	}
}

func TestResolveContextEnvVar(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvContext, "staging")

	resolved, err := config.Resolve(config.Flags{}, twoContextCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://staging" {
		t.Errorf("server: got %q, want the POND_CONTEXT context", resolved.Server)
	}
}

func TestResolveContextFlagBeatsContextEnv(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvContext, "staging")

	resolved, err := config.Resolve(config.Flags{Context: "prod"}, twoContextCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Server != "http://prod" {
		t.Errorf("server: got %q, want the --context context", resolved.Server)
	}
}

func TestResolveUnknownContextFromEnv(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvContext, "nonexistent")

	if _, err := config.Resolve(config.Flags{}, twoContextCfg()); err == nil {
		t.Error("expected error for unknown context")
	}
}

func portForwardCfg() *config.Config {
	return &config.Config{
		CurrentContext: "ctx",
		Contexts: map[string]config.Context{
			"ctx": {
				Server: "http://ctx-server",
				PortForward: config.PortForward{
					KubeContext:   "ctx-kube",
					KubeNamespace: "ctx-ns",
					KubeService:   "ctx-svc",
					Port:          11111,
				},
			},
		},
	}
}

func TestResolvePortForwardFromContext(t *testing.T) {
	clearPondEnv(t)
	resolved, err := config.Resolve(config.Flags{}, portForwardCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := config.PortForward{KubeContext: "ctx-kube", KubeNamespace: "ctx-ns", KubeService: "ctx-svc", Port: 11111}
	if resolved.PortForward != want {
		t.Errorf("port-forward: got %+v, want %+v", resolved.PortForward, want)
	}
}

func TestResolvePortForwardFromEnv(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvKubeContext, "env-kube")
	t.Setenv(config.EnvKubeNamespace, "env-ns")
	t.Setenv(config.EnvKubeService, "env-svc")
	t.Setenv(config.EnvForwardPort, "24680")

	resolved, err := config.Resolve(config.Flags{}, portForwardCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := config.PortForward{KubeContext: "env-kube", KubeNamespace: "env-ns", KubeService: "env-svc", Port: 24680}
	if resolved.PortForward != want {
		t.Errorf("port-forward: got %+v, want %+v", resolved.PortForward, want)
	}
}

func TestResolvePortForwardFromFlags(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvKubeContext, "env-kube")
	t.Setenv(config.EnvKubeNamespace, "env-ns")
	t.Setenv(config.EnvKubeService, "env-svc")
	t.Setenv(config.EnvForwardPort, "24680")

	flags := config.Flags{PortForward: config.PortForward{
		KubeContext: "flag-kube", KubeNamespace: "flag-ns", KubeService: "flag-svc", Port: 32000,
	}}
	resolved, err := config.Resolve(flags, portForwardCfg())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := config.PortForward{KubeContext: "flag-kube", KubeNamespace: "flag-ns", KubeService: "flag-svc", Port: 32000}
	if resolved.PortForward != want {
		t.Errorf("port-forward: got %+v, want %+v", resolved.PortForward, want)
	}
}

// KubeService must stay empty here: portforward.go applies the pond-server default, and
// pre-defaulting it in Resolve would make PortForward never compare as the zero value.
func TestResolvePortForwardDefaults(t *testing.T) {
	clearPondEnv(t)
	resolved, err := config.Resolve(config.Flags{}, &config.Config{Contexts: map[string]config.Context{}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.PortForward != (config.PortForward{}) {
		t.Errorf("port-forward: got %+v, want zero value", resolved.PortForward)
	}
	if resolved.PortForward.Configured() {
		t.Error("expected an unconfigured port-forward")
	}
}

func TestResolvePortForwardPartialEnv(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvKubeContext, "env-kube")
	t.Setenv(config.EnvKubeNamespace, "env-ns")

	resolved, err := config.Resolve(config.Flags{}, &config.Config{Contexts: map[string]config.Context{}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.PortForward.Configured() {
		t.Error("expected an unconfigured port-forward when the port is missing")
	}
}

func TestResolvePortForwardWithoutContextFile(t *testing.T) {
	clearPondEnv(t)
	t.Setenv(config.EnvKubeContext, "env-kube")
	t.Setenv(config.EnvKubeNamespace, "env-ns")
	t.Setenv(config.EnvForwardPort, "24680")

	resolved, err := config.Resolve(config.Flags{}, &config.Config{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !resolved.PortForward.Configured() {
		t.Errorf("expected a configured port-forward from env alone, got %+v", resolved.PortForward)
	}
}

func TestResolvePortForwardInvalidPort(t *testing.T) {
	for _, value := range []string{"abc", "0", "-1", "70000"} {
		t.Run(value, func(t *testing.T) {
			clearPondEnv(t)
			t.Setenv(config.EnvForwardPort, value)

			_, err := config.Resolve(config.Flags{}, &config.Config{})
			if err == nil {
				t.Fatalf("expected an error for %s=%q", config.EnvForwardPort, value)
			}
			if !strings.Contains(err.Error(), config.EnvForwardPort) {
				t.Errorf("error should name %s, got: %v", config.EnvForwardPort, err)
			}
		})
	}
}

func TestConfigPathFromEnv(t *testing.T) {
	clearPondEnv(t)
	want := filepath.Join(t.TempDir(), "e2e-config")
	t.Setenv(config.EnvConfig, want)

	got, err := config.ConfigPath()
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if got != want {
		t.Errorf("config path: got %q, want %q", got, want)
	}
}

func TestConfigPathDefault(t *testing.T) {
	clearPondEnv(t)
	got, err := config.ConfigPath()
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if want := filepath.Join(".pond", "config"); !strings.HasSuffix(got, want) {
		t.Errorf("config path: got %q, want it to end in %q", got, want)
	}
}

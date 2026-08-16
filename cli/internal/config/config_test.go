package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pondplatform/pond/cli/internal/config"
)

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
}

func TestResolveContext(t *testing.T) {
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
	cfg := &config.Config{Contexts: map[string]config.Context{}}
	_, err := config.Resolve(config.Flags{Context: "nonexistent"}, cfg)
	if err == nil {
		t.Error("expected error for unknown context")
	}
}

func TestResolveNamedContextFlagOverride(t *testing.T) {
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

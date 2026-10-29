package tofu

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_MissingModuleNamesTheDependencyType(t *testing.T) {
	err := NewRunner().Init(context.Background(), "/opt/pond/tofu-providers/redis", io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `no provider module "redis"`) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDestroy_CreatesTheStateDirectory(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no tofu: only the preparation runs
	statePath := filepath.Join(t.TempDir(), "env", "api", "db", "terraform.tfstate")

	_ = NewRunner().Destroy(context.Background(), t.TempDir(), statePath, map[string]any{}, io.Discard)

	if info, err := os.Stat(filepath.Dir(statePath)); err != nil || !info.IsDir() {
		t.Errorf("state directory not created: %v", err)
	}
}

package tofu

import (
	"context"
	"io"
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

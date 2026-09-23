package app

import (
	"strings"
	"testing"

	pondconfig "github.com/pondplatform/pond/cli/internal/config"
)

func TestMaybeStartPortForwardNothingConfigured(t *testing.T) {
	if err := maybeStartPortForward(pondconfig.PortForward{}); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// A stray kube-service on its own should not nag: it is the one optional field.
func TestMaybeStartPortForwardServiceOnly(t *testing.T) {
	pf := pondconfig.PortForward{KubeService: "pond-server"}
	if err := maybeStartPortForward(pf); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestMaybeStartPortForwardIncomplete(t *testing.T) {
	pf := pondconfig.PortForward{KubeContext: "my-cluster"}
	err := maybeStartPortForward(pf)
	if err == nil {
		t.Fatal("expected an error for an incomplete port-forward config")
	}
	for _, want := range []string{"kube-context", "kube-namespace", "port"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

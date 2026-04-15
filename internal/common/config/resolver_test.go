package config_test

import (
	"testing"

	"github.com/pondplatform/pond/internal/common/config"
	"github.com/pondplatform/pond/internal/common/domain"
)

func ptr[T any](v T) *T { return &v }

func TestResolve_NoOverrideForEnv_ReturnsBase(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			Name:  "my-service",
			Image: "my-image:latest",
		},
		Overrides: map[string]domain.Override{},
	}

	got, err := r.Resolve(base, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "my-service" {
		t.Errorf("expected Name %q, got %q", "my-service", got.Name)
	}
}

func TestResolve_NilMaps_Initialized(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			// Env, Dependencies, Configs are all nil
		},
		Overrides: map[string]domain.Override{},
	}

	got, err := r.Resolve(base, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Env == nil {
		t.Error("Env should be initialized to empty map, got nil")
	}
	if got.Dependencies == nil {
		t.Error("Dependencies should be initialized to empty map, got nil")
	}
	if got.Configs == nil {
		t.Error("Configs should be initialized to empty map, got nil")
	}
}

func TestResolve_IngressOverrideApplied(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			Ingress: domain.IngressConfig{Enabled: false},
		},
		Overrides: map[string]domain.Override{
			"prod": {
				Ingress: &domain.IngressOverride{Enabled: ptr(true)},
			},
		},
	}

	got, err := r.Resolve(base, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Ingress.Enabled {
		t.Error("expected Ingress.Enabled = true after override")
	}
}

func TestResolve_ReplicasOverrideApplied(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			Service: domain.ServiceSpec{Replicas: 1},
		},
		Overrides: map[string]domain.Override{
			"prod": {
				Service: &domain.ServiceOverride{Replicas: ptr(int32(5))},
			},
		},
	}

	got, err := r.Resolve(base, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Service.Replicas != 5 {
		t.Errorf("expected Replicas = 5, got %d", got.Service.Replicas)
	}
}

func TestResolve_EnvVarsDeepMerge(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			Env: map[string]string{"A": "base-a", "B": "base-b"},
		},
		Overrides: map[string]domain.Override{
			"prod": {
				Env: map[string]string{"B": "override-b", "C": "new-c"},
			},
		},
	}

	got, err := r.Resolve(base, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Env["A"] != "base-a" {
		t.Errorf("expected A=%q, got %q", "base-a", got.Env["A"])
	}
	if got.Env["B"] != "override-b" {
		t.Errorf("expected B=%q (overridden), got %q", "override-b", got.Env["B"])
	}
	if got.Env["C"] != "new-c" {
		t.Errorf("expected C=%q (new key), got %q", "new-c", got.Env["C"])
	}
}

func TestResolve_OverrideBleeding_OtherEnvUnaffected(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			Service: domain.ServiceSpec{Replicas: 1},
		},
		Overrides: map[string]domain.Override{
			"prod": {
				Service: &domain.ServiceOverride{Replicas: ptr(int32(10))},
			},
		},
	}

	// Resolve prod — should have 10 replicas.
	prod, err := r.Resolve(base, "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prod.Service.Replicas != 10 {
		t.Errorf("expected prod Replicas = 10, got %d", prod.Service.Replicas)
	}

	// Resolve staging — should still have 1 replica (prod override did not bleed).
	staging, err := r.Resolve(base, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if staging.Service.Replicas != 1 {
		t.Errorf("expected staging Replicas = 1, got %d", staging.Service.Replicas)
	}
}

func TestResolve_OverrideNilPointers_BaseNotMutated(t *testing.T) {
	r := config.NewResolver()
	base := &domain.OverridableConfig{
		ServiceConfig: domain.ServiceConfig{
			Ingress: domain.IngressConfig{Enabled: true},
			Service: domain.ServiceSpec{Replicas: 3},
		},
		Overrides: map[string]domain.Override{
			// Override struct exists but all pointer fields are nil.
			"staging": {},
		},
	}

	got, err := r.Resolve(base, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Ingress.Enabled {
		t.Error("expected Ingress.Enabled to remain true when override is empty")
	}
	if got.Service.Replicas != 3 {
		t.Errorf("expected Replicas = 3, got %d", got.Service.Replicas)
	}
}

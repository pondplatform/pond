package config_test

import (
	"testing"

	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

func pathRule(value string) serviceconfig.HTTPRouteRule {
	return serviceconfig.HTTPRouteRule{
		Matches: []serviceconfig.HTTPRouteMatch{{
			Path: &serviceconfig.HTTPPathMatch{
				Type:  serviceconfig.Ptr("PathPrefix"),
				Value: serviceconfig.Ptr(value),
			},
		}},
	}
}

func TestResolve_DeploymentReplicasOverridden(t *testing.T) {
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name:       "api",
			Deployment: &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(1))},
			Service:    &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))},
		},
		Overrides: map[string]serviceconfig.Override{
			"production": {
				Deployment: &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(3))},
			},
		},
	}

	cfg, err := config.NewResolver().Resolve(base, "production")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if *cfg.Deployment.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %d", *cfg.Deployment.Replicas)
	}
	if *cfg.Service.Port != 8080 {
		t.Errorf("port should be untouched, got %d", *cfg.Service.Port)
	}
	// The base must not be mutated by resolution.
	if *base.Deployment.Replicas != 1 {
		t.Errorf("base replicas were mutated: %d", *base.Deployment.Replicas)
	}
}

func TestResolve_GatewayRulesReplaceRatherThanAppend(t *testing.T) {
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name: "api",
			Gateway: &serviceconfig.GatewayConfig{
				HTTP: &serviceconfig.GatewayHTTPConfig{
					Enabled:  serviceconfig.Ptr(true),
					Hostname: serviceconfig.Ptr("api.staging.example.com"),
					Rules:    []serviceconfig.HTTPRouteRule{pathRule("/")},
				},
			},
		},
		Overrides: map[string]serviceconfig.Override{
			"production": {
				Gateway: &serviceconfig.GatewayConfig{
					HTTP: &serviceconfig.GatewayHTTPConfig{
						Rules: []serviceconfig.HTTPRouteRule{pathRule("/v2")},
					},
				},
			},
		},
	}

	cfg, err := config.NewResolver().Resolve(base, "production")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(cfg.Gateway.HTTP.Rules) != 1 {
		t.Fatalf("rules should replace, not append; got %d", len(cfg.Gateway.HTTP.Rules))
	}
	if *cfg.Gateway.HTTP.Rules[0].Matches[0].Path.Value != "/v2" {
		t.Errorf("unexpected rule: %+v", cfg.Gateway.HTTP.Rules[0])
	}

	// Fields absent from the override keep their base values.
	if *cfg.Gateway.HTTP.Hostname != "api.staging.example.com" {
		t.Errorf("hostname should be inherited, got %q", *cfg.Gateway.HTTP.Hostname)
	}
	if !*cfg.Gateway.HTTP.Enabled {
		t.Error("enabled should be inherited as true")
	}
	if *base.Gateway.HTTP.Rules[0].Matches[0].Path.Value != "/" {
		t.Error("base rules were mutated")
	}
}

func TestResolve_GatewayEnabledToggledOffPerEnvironment(t *testing.T) {
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name: "api",
			Gateway: &serviceconfig.GatewayConfig{
				HTTP: &serviceconfig.GatewayHTTPConfig{
					Enabled: serviceconfig.Ptr(true),
					Rules:   []serviceconfig.HTTPRouteRule{pathRule("/")},
				},
			},
		},
		Overrides: map[string]serviceconfig.Override{
			"dev": {
				Gateway: &serviceconfig.GatewayConfig{
					HTTP: &serviceconfig.GatewayHTTPConfig{
						Enabled: serviceconfig.Ptr(false),
					},
				},
			},
		},
	}

	cfg, err := config.NewResolver().Resolve(base, "dev")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if *cfg.Gateway.HTTP.Enabled {
		t.Error("expected gateway to be disabled in dev")
	}
	if len(cfg.Gateway.HTTP.Rules) != 1 {
		t.Errorf("rules should be inherited, got %d", len(cfg.Gateway.HTTP.Rules))
	}
}

func TestResolve_NoOverrideForEnvironmentLeavesBaseIntact(t *testing.T) {
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name:       "api",
			Deployment: &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(2))},
		},
	}

	cfg, err := config.NewResolver().Resolve(base, "production")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if *cfg.Deployment.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %d", *cfg.Deployment.Replicas)
	}
}

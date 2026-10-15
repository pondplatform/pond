package config_test

import (
	"maps"
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

func TestResolve_ManagementProbeReplacedPerEnvironment(t *testing.T) {
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name: "api",
			Deployment: &serviceconfig.DeploymentSpec{
				Replicas: serviceconfig.Ptr(int32(1)),
				Management: &serviceconfig.ManagementConfig{
					LivenessProbe: &serviceconfig.Probe{
						HTTPGet:       &serviceconfig.HTTPGetAction{Path: "/health/live"},
						PeriodSeconds: serviceconfig.Ptr(int32(30)),
					},
					ReadinessProbe: &serviceconfig.Probe{
						HTTPGet: &serviceconfig.HTTPGetAction{Path: "/health/ready"},
					},
				},
			},
		},
		Overrides: map[string]serviceconfig.Override{
			"production": {
				Deployment: &serviceconfig.DeploymentSpec{
					Management: &serviceconfig.ManagementConfig{
						LivenessProbe: &serviceconfig.Probe{
							HTTPGet: &serviceconfig.HTTPGetAction{Path: "/health/live-slow"},
						},
					},
				},
			},
		},
	}

	cfg, err := config.NewResolver().Resolve(base, "production")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	mgmt := cfg.Deployment.Management
	if mgmt.LivenessProbe.HTTPGet.Path != "/health/live-slow" {
		t.Errorf("unexpected liveness path: %q", mgmt.LivenessProbe.HTTPGet.Path)
	}
	// The probe is replaced wholesale, so base timings do not survive alongside
	// the override's path.
	if mgmt.LivenessProbe.PeriodSeconds != nil {
		t.Errorf("base periodSeconds should not survive a replaced probe, got %d", *mgmt.LivenessProbe.PeriodSeconds)
	}
	// Sub-blocks absent from the override keep their base values.
	if mgmt.ReadinessProbe == nil || mgmt.ReadinessProbe.HTTPGet.Path != "/health/ready" {
		t.Errorf("readiness probe should be inherited, got %+v", mgmt.ReadinessProbe)
	}
	if *cfg.Deployment.Replicas != 1 {
		t.Errorf("replicas should be untouched, got %d", *cfg.Deployment.Replicas)
	}
	// The base must not be mutated by resolution.
	if base.Deployment.Management.LivenessProbe.HTTPGet.Path != "/health/live" {
		t.Errorf("base liveness probe was mutated: %q", base.Deployment.Management.LivenessProbe.HTTPGet.Path)
	}
}

func TestResolve_OverridesDoNotLeakIntoLaterEnvironments(t *testing.T) {
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name:         "api",
			Env:          map[string]string{"LOG_LEVEL": "info"},
			Dependencies: map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}},
			Configs: map[string]serviceconfig.ConfigFileSpec{
				"app.json": {Format: "json", MountPath: "/etc/app.json"},
			},
		},
		Overrides: map[string]serviceconfig.Override{
			"staging": {
				Env:          map[string]string{"LOG_LEVEL": "debug", "STAGING_ONLY": "1"},
				Dependencies: map[string]serviceconfig.DependencyDeclaration{"cache": {Type: "redis"}},
				Configs: map[string]serviceconfig.ConfigFileSpec{
					"debug.env": {Format: ".env", MountPath: "/etc/debug.env"},
				},
			},
		},
	}
	resolver := config.NewResolver()

	if _, err := resolver.Resolve(base, "staging"); err != nil {
		t.Fatalf("Resolve staging failed: %v", err)
	}
	prod, err := resolver.Resolve(base, "production")
	if err != nil {
		t.Fatalf("Resolve production failed: %v", err)
	}
	// Writing to the result must not reach base either.
	prod.Env["PROD_ONLY"] = "1"

	if !maps.Equal(base.Env, map[string]string{"LOG_LEVEL": "info"}) {
		t.Errorf("base env was mutated: %v", base.Env)
	}
	if want := map[string]string{"LOG_LEVEL": "info", "PROD_ONLY": "1"}; !maps.Equal(prod.Env, want) {
		t.Errorf("production env: want %v, got %v", want, prod.Env)
	}
	if _, ok := prod.Dependencies["cache"]; ok {
		t.Error("staging dependency leaked into production")
	}
	if _, ok := prod.Configs["debug.env"]; ok {
		t.Error("staging config leaked into production")
	}
}

func TestResolve_VolumesAreTheSameInEveryEnvironment(t *testing.T) {
	data := serviceconfig.VolumeSpec{Type: "shared-single-node", MountPath: "/data", Size: "10Gi"}
	base := &serviceconfig.OverridableConfig{
		ServiceConfig: serviceconfig.ServiceConfig{
			Name:    "api",
			Volumes: map[string]serviceconfig.VolumeSpec{"data": data},
		},
		Overrides: map[string]serviceconfig.Override{
			"production": {Env: map[string]string{"LOG_LEVEL": "warn"}},
		},
	}
	resolver := config.NewResolver()

	for _, env := range []string{"production", "staging"} {
		cfg, err := resolver.Resolve(base, env)
		if err != nil {
			t.Fatalf("Resolve %s failed: %v", env, err)
		}
		if want := map[string]serviceconfig.VolumeSpec{"data": data}; !maps.Equal(cfg.Volumes, want) {
			t.Errorf("%s volumes: want %v, got %v", env, want, cfg.Volumes)
		}
		// Writing to the result must not reach base.
		cfg.Volumes["scratch"] = serviceconfig.VolumeSpec{Type: "shared-single-node", MountPath: "/scratch", Size: "1Gi"}
	}
	if len(base.Volumes) != 1 {
		t.Errorf("base volumes were mutated: %v", base.Volumes)
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

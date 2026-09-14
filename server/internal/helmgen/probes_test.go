package helmgen

import (
	"errors"
	"strings"
	"testing"

	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

func managementConfig(name string, mgmt *serviceconfig.ManagementConfig, servicePort *int32) *serviceconfig.ServiceConfig {
	cfg := &serviceconfig.ServiceConfig{
		Name:       name,
		Deployment: &serviceconfig.DeploymentSpec{Management: mgmt},
	}
	if servicePort != nil {
		cfg.Service = &serviceconfig.ServiceSpec{Port: servicePort}
	}
	return cfg
}

// Regression guard: probes are opt-in. If the values file carries no probe keys
// the chart configures none, so an undeclared probe must never appear here -
// the chart used to default both to `path: /`, failing services that 404 on /.
func TestGenerator_Generate_OmitsProbesWhenUnset(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name:    "web",
		Service: &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))},
	}

	vals, err := g.Generate(cfg, &domain.Environment{}, "")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.LivenessProbe != nil || vals.ReadinessProbe != nil {
		t.Errorf("Expected no probes, got liveness=%+v readiness=%+v", vals.LivenessProbe, vals.ReadinessProbe)
	}

	out, err := yaml.Marshal(vals)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	for _, key := range []string{"livenessProbe", "readinessProbe"} {
		if strings.Contains(string(out), key) {
			t.Errorf("%s should not appear in the values file:\n%s", key, out)
		}
	}
}

// An empty management block must not conjure probes either.
func TestGenerator_Generate_OmitsProbesWhenManagementEmpty(t *testing.T) {
	g := NewGenerator()

	cfg := managementConfig("web", &serviceconfig.ManagementConfig{}, serviceconfig.Ptr(int32(8080)))

	vals, err := g.Generate(cfg, &domain.Environment{}, "")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.LivenessProbe != nil || vals.ReadinessProbe != nil {
		t.Errorf("Expected no probes, got liveness=%+v readiness=%+v", vals.LivenessProbe, vals.ReadinessProbe)
	}
}

// Declaring one probe must not imply the other: liveness and readiness are
// independent, unlike the old schema which pointed both at one endpoint.
func TestGenerator_Generate_ReadinessOnlyLeavesLivenessUnset(t *testing.T) {
	g := NewGenerator()

	cfg := managementConfig("web", &serviceconfig.ManagementConfig{
		ReadinessProbe: &serviceconfig.Probe{
			HTTPGet: &serviceconfig.HTTPGetAction{Path: "/health/ready"},
		},
	}, serviceconfig.Ptr(int32(8080)))

	vals, err := g.Generate(cfg, &domain.Environment{}, "")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.LivenessProbe != nil {
		t.Errorf("Expected liveness probe to stay unset, got %+v", vals.LivenessProbe)
	}
	if vals.ReadinessProbe == nil {
		t.Fatal("Expected a readiness probe")
	}
	if vals.ReadinessProbe.HTTPGet.Path != "/health/ready" {
		t.Errorf("Unexpected readiness path: %s", vals.ReadinessProbe.HTTPGet.Path)
	}
	// Port is unset on the probe, so it falls back to service.port.
	if vals.ReadinessProbe.HTTPGet.Port != 8080 {
		t.Errorf("Expected readiness port to default to service.port 8080, got %d", vals.ReadinessProbe.HTTPGet.Port)
	}
}

func TestGenerator_Generate_ProbeFieldsRoundTrip(t *testing.T) {
	g := NewGenerator()

	cfg := managementConfig("web", &serviceconfig.ManagementConfig{
		LivenessProbe: &serviceconfig.Probe{
			HTTPGet: &serviceconfig.HTTPGetAction{
				Path:   "/health/live",
				Port:   serviceconfig.Ptr(int32(9090)),
				Scheme: serviceconfig.Ptr("HTTPS"),
				HTTPHeaders: []serviceconfig.HTTPHeader{
					{Name: "X-Probe", Value: "1"},
				},
			},
			InitialDelaySeconds: serviceconfig.Ptr(int32(5)),
			PeriodSeconds:       serviceconfig.Ptr(int32(15)),
			TimeoutSeconds:      serviceconfig.Ptr(int32(2)),
			FailureThreshold:    serviceconfig.Ptr(int32(3)),
		},
	}, serviceconfig.Ptr(int32(8080)))

	vals, err := g.Generate(cfg, &domain.Environment{}, "")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out, err := yaml.Marshal(vals.LivenessProbe)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	expected := `httpGet:
    path: /health/live
    port: 9090
    scheme: HTTPS
    httpHeaders:
        - name: X-Probe
          value: "1"
initialDelaySeconds: 5
periodSeconds: 15
timeoutSeconds: 2
failureThreshold: 3
`
	if string(out) != expected {
		t.Errorf("Unexpected liveness probe values:\nExpected:\n%s\nGot:\n%s", expected, out)
	}

	// successThreshold was not set, so it must not be pinned in the values.
	if strings.Contains(string(out), "successThreshold") {
		t.Errorf("successThreshold should be omitted when unset:\n%s", out)
	}
}

func TestGenerator_Generate_MetricsAnnotationsUseServicePort(t *testing.T) {
	g := NewGenerator()

	cfg := managementConfig("web", &serviceconfig.ManagementConfig{
		Metrics: &serviceconfig.MetricsConfig{Endpoint: "/metrics"},
	}, serviceconfig.Ptr(int32(8080)))

	vals, err := g.Generate(cfg, &domain.Environment{}, "")
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	want := map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/port":   "8080",
		"prometheus.io/path":   "/metrics",
	}
	for k, v := range want {
		if vals.PodAnnotations[k] != v {
			t.Errorf("Annotation %s: expected %q, got %q", k, v, vals.PodAnnotations[k])
		}
	}
}

func TestValidateManagement_Valid(t *testing.T) {
	cfg := managementConfig("web", &serviceconfig.ManagementConfig{
		LivenessProbe: &serviceconfig.Probe{
			HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/health/live"},
			SuccessThreshold: serviceconfig.Ptr(int32(1)),
		},
		ReadinessProbe: &serviceconfig.Probe{
			HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/health/ready", Scheme: serviceconfig.Ptr("HTTP")},
			SuccessThreshold: serviceconfig.Ptr(int32(2)),
		},
		Metrics: &serviceconfig.MetricsConfig{Endpoint: "/metrics", Port: serviceconfig.Ptr(int32(9090))},
	}, serviceconfig.Ptr(int32(8080)))

	if err := ValidateManagement(cfg); err != nil {
		t.Errorf("Expected valid config, got: %v", err)
	}
}

func TestValidateManagement_NoManagementIsValid(t *testing.T) {
	if err := ValidateManagement(&serviceconfig.ServiceConfig{Name: "web"}); err != nil {
		t.Errorf("Expected a config without management to be valid, got: %v", err)
	}
}

func TestValidateManagement_Errors(t *testing.T) {
	tests := []struct {
		name       string
		mgmt       *serviceconfig.ManagementConfig
		portOnSvc  *int32
		wantFields []string
	}{
		{
			name: "port unresolvable without service port",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "/ready"},
				},
			},
			wantFields: []string{"readinessProbe.httpGet.port"},
		},
		{
			name: "path without leading slash",
			mgmt: &serviceconfig.ManagementConfig{
				LivenessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "health"},
				},
			},
			portOnSvc:  serviceconfig.Ptr(int32(8080)),
			wantFields: []string{"livenessProbe.httpGet.path"},
		},
		{
			name: "missing httpGet",
			mgmt: &serviceconfig.ManagementConfig{
				LivenessProbe: &serviceconfig.Probe{},
			},
			portOnSvc:  serviceconfig.Ptr(int32(8080)),
			wantFields: []string{"livenessProbe.httpGet"},
		},
		{
			name: "unsupported scheme",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "/ready", Scheme: serviceconfig.Ptr("TCP")},
				},
			},
			portOnSvc:  serviceconfig.Ptr(int32(8080)),
			wantFields: []string{"readinessProbe.httpGet.scheme"},
		},
		{
			name: "liveness successThreshold must be 1",
			mgmt: &serviceconfig.ManagementConfig{
				LivenessProbe: &serviceconfig.Probe{
					HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/live"},
					SuccessThreshold: serviceconfig.Ptr(int32(2)),
				},
			},
			portOnSvc:  serviceconfig.Ptr(int32(8080)),
			wantFields: []string{"livenessProbe.successThreshold"},
		},
		{
			name: "port out of range",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "/ready", Port: serviceconfig.Ptr(int32(70000))},
				},
			},
			wantFields: []string{"readinessProbe.httpGet.port"},
		},
		{
			name: "non-positive timings",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/ready"},
					PeriodSeconds:    serviceconfig.Ptr(int32(0)),
					FailureThreshold: serviceconfig.Ptr(int32(0)),
				},
			},
			portOnSvc:  serviceconfig.Ptr(int32(8080)),
			wantFields: []string{"readinessProbe.periodSeconds", "readinessProbe.failureThreshold"},
		},
		{
			name: "metrics endpoint without resolvable port",
			mgmt: &serviceconfig.ManagementConfig{
				Metrics: &serviceconfig.MetricsConfig{Endpoint: "metrics"},
			},
			wantFields: []string{"metrics.endpoint", "metrics.port"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManagement(managementConfig("web", tc.mgmt, tc.portOnSvc))
			if err == nil {
				t.Fatal("Expected a validation error")
			}

			var ve *api.ValidationErrors
			if !errors.As(err, &ve) {
				t.Fatalf("Expected *api.ValidationErrors, got %T: %v", err, err)
			}

			got := make(map[string]bool, len(ve.Errors))
			for _, e := range ve.Errors {
				if e.Component != "deployment.management" {
					t.Errorf("Unexpected component %q on %+v", e.Component, e)
				}
				got[e.Field] = true
			}
			for _, field := range tc.wantFields {
				if !got[field] {
					t.Errorf("Expected an error on %q, got %+v", field, ve.Errors)
				}
			}
			if len(ve.Errors) != len(tc.wantFields) {
				t.Errorf("Expected %d errors, got %+v", len(tc.wantFields), ve.Errors)
			}
		})
	}
}

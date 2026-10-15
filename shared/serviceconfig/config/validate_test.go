package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// validConfig is the smallest config Validate accepts. Tests mutate a copy of it
// so each case only carries the field it is about.
func validConfig() *serviceconfig.ServiceConfig {
	return &serviceconfig.ServiceConfig{
		Version: 1,
		Name:    "my-service",
		Image:   "ghcr.io/example/my-service",
		Service: &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))},
	}
}

// wantFields asserts that Validate reported errors on exactly these fields.
func wantFields(t *testing.T, cfg *serviceconfig.ServiceConfig, fields ...string) {
	t.Helper()

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Expected a validation error on %v", fields)
	}

	var ve *api.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("Expected *api.ValidationErrors, got %T: %v", err, err)
	}

	got := make(map[string]bool, len(ve.Errors))
	for _, e := range ve.Errors {
		if e.Component != validationComponent {
			t.Errorf("Unexpected component %q on %+v", e.Component, e)
		}
		got[e.Field] = true
	}
	for _, field := range fields {
		if !got[field] {
			t.Errorf("Expected an error on %q, got %+v", field, ve.Errors)
		}
	}
	if len(ve.Errors) != len(fields) {
		t.Errorf("Expected %d errors, got %+v", len(fields), ve.Errors)
	}
}

func TestValidate_MinimalConfigIsValid(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Errorf("Expected valid config, got: %v", err)
	}
}

func TestValidate_FullConfigIsValid(t *testing.T) {
	cfg := validConfig()
	cfg.Deployment = &serviceconfig.DeploymentSpec{
		Replicas: serviceconfig.Ptr(int32(3)),
		Management: &serviceconfig.ManagementConfig{
			LivenessProbe: &serviceconfig.Probe{
				HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/health/live"},
				SuccessThreshold: serviceconfig.Ptr(int32(1)),
			},
			ReadinessProbe: &serviceconfig.Probe{
				HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/health/ready", Scheme: serviceconfig.Ptr("HTTP")},
				SuccessThreshold: serviceconfig.Ptr(int32(2)),
			},
			Metrics: &serviceconfig.MetricsConfig{Endpoint: "/metrics", Port: serviceconfig.Ptr(int32(9090))},
		},
	}
	cfg.Gateway = &serviceconfig.GatewayConfig{
		HTTP: &serviceconfig.GatewayHTTPConfig{
			Enabled:  serviceconfig.Ptr(true),
			Hostname: serviceconfig.Ptr("my-service.example.com"),
			Rules: []serviceconfig.HTTPRouteRule{{
				Matches: []serviceconfig.HTTPRouteMatch{{
					Path:    &serviceconfig.HTTPPathMatch{Type: serviceconfig.Ptr("PathPrefix"), Value: serviceconfig.Ptr("/api")},
					Headers: []serviceconfig.HTTPHeaderMatch{{Name: "X-Internal", Value: "true"}},
					Method:  serviceconfig.Ptr("GET"),
				}},
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type: "URLRewrite",
					URLRewrite: &serviceconfig.HTTPURLRewriteFilter{
						Path: &serviceconfig.HTTPPathModifier{
							Type:               "ReplacePrefixMatch",
							ReplacePrefixMatch: serviceconfig.Ptr("/"),
						},
					},
				}},
			}},
		},
	}
	cfg.Env = map[string]string{"LOG_LEVEL": "info"}
	cfg.Dependencies = map[string]serviceconfig.DependencyDeclaration{"db": {Type: "postgres"}}
	cfg.Configs = map[string]serviceconfig.ConfigFileSpec{
		".env": {Format: ".env", MountPath: "/app/.env", Values: map[string]any{"DB_HOST": "{{dependencies.db.host}}"}},
	}
	cfg.Volumes = map[string]serviceconfig.VolumeSpec{
		"data": {Type: "shared-single-node", MountPath: "/var/lib/my-service", Size: "10Gi"},
	}
	cfg.Command = []string{"serve", "--port", "{{service.port}}"}
	cfg.Variables = map[string]serviceconfig.VariableDeclaration{"SIGNING_KEY": {Secret: true}}

	if err := Validate(cfg); err != nil {
		t.Errorf("Expected valid config, got: %v", err)
	}
}

func TestValidate_Command(t *testing.T) {
	tests := []struct {
		name       string
		command    []string
		wantFields []string
	}{
		{name: "unset keeps the image's default command", command: nil},
		{name: "one argument", command: []string{"start"}},
		{name: "an empty argument is still an argument", command: []string{"serve", "--prefix", ""}},
		{
			// Compose reads an empty list as "no arguments", which can't be honoured.
			name:       "empty list",
			command:    []string{},
			wantFields: []string{"command"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Command = tc.command
			if len(tc.wantFields) == 0 {
				if err := Validate(cfg); err != nil {
					t.Errorf("Expected valid config, got: %v", err)
				}
				return
			}
			wantFields(t, cfg, tc.wantFields...)
		})
	}
}

func TestValidate_Identity(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*serviceconfig.ServiceConfig)
		wantFields []string
	}{
		{
			name:       "version missing",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Version = 0 },
			wantFields: []string{"version"},
		},
		{
			name:       "unsupported version",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Version = 2 },
			wantFields: []string{"version"},
		},
		{
			name:       "name missing",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Name = "" },
			wantFields: []string{"name"},
		},
		{
			name:       "name is not a DNS label",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Name = "My_Service" },
			wantFields: []string{"name"},
		},
		{
			// The name becomes the Helm release name, which Helm caps at 53 characters.
			name: "name too long",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Name = "a-very-long-service-name-that-helm-will-refuse-to-install"
			},
			wantFields: []string{"name"},
		},
		{
			name:       "neither image nor build",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Image = "" },
			wantFields: []string{"image"},
		},
		{
			name: "build stands in for image",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Image = ""
				c.Build = "./Dockerfile"
			},
		},
		{
			name:       "service port out of range",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Service.Port = serviceconfig.Ptr(int32(70000)) },
			wantFields: []string{"service.port"},
		},
		{
			name: "negative replicas",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Deployment = &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(-1))}
			},
			wantFields: []string{"deployment.replicas"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(cfg)

			if len(tc.wantFields) == 0 {
				if err := Validate(cfg); err != nil {
					t.Errorf("Expected valid config, got: %v", err)
				}
				return
			}
			wantFields(t, cfg, tc.wantFields...)
		})
	}
}

func TestValidate_Management(t *testing.T) {
	const prefix = "deployment.management."

	tests := []struct {
		name        string
		mgmt        *serviceconfig.ManagementConfig
		servicePort *int32
		wantFields  []string
	}{
		{
			name: "no management is valid",
		},
		{
			name: "port unresolvable without service port",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "/ready"},
				},
			},
			wantFields: []string{prefix + "readinessProbe.httpGet.port"},
		},
		{
			name: "path without leading slash",
			mgmt: &serviceconfig.ManagementConfig{
				LivenessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "health"},
				},
			},
			servicePort: serviceconfig.Ptr(int32(8080)),
			wantFields:  []string{prefix + "livenessProbe.httpGet.path"},
		},
		{
			name: "missing httpGet",
			mgmt: &serviceconfig.ManagementConfig{
				LivenessProbe: &serviceconfig.Probe{},
			},
			servicePort: serviceconfig.Ptr(int32(8080)),
			wantFields:  []string{prefix + "livenessProbe.httpGet"},
		},
		{
			name: "unsupported scheme",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "/ready", Scheme: serviceconfig.Ptr("TCP")},
				},
			},
			servicePort: serviceconfig.Ptr(int32(8080)),
			wantFields:  []string{prefix + "readinessProbe.httpGet.scheme"},
		},
		{
			name: "liveness successThreshold must be 1",
			mgmt: &serviceconfig.ManagementConfig{
				LivenessProbe: &serviceconfig.Probe{
					HTTPGet:          &serviceconfig.HTTPGetAction{Path: "/live"},
					SuccessThreshold: serviceconfig.Ptr(int32(2)),
				},
			},
			servicePort: serviceconfig.Ptr(int32(8080)),
			wantFields:  []string{prefix + "livenessProbe.successThreshold"},
		},
		{
			name: "probe port out of range",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{Path: "/ready", Port: serviceconfig.Ptr(int32(70000))},
				},
			},
			wantFields: []string{prefix + "readinessProbe.httpGet.port"},
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
			servicePort: serviceconfig.Ptr(int32(8080)),
			wantFields:  []string{prefix + "readinessProbe.periodSeconds", prefix + "readinessProbe.failureThreshold"},
		},
		{
			name: "probe header without a name",
			mgmt: &serviceconfig.ManagementConfig{
				ReadinessProbe: &serviceconfig.Probe{
					HTTPGet: &serviceconfig.HTTPGetAction{
						Path:        "/ready",
						HTTPHeaders: []serviceconfig.HTTPHeader{{Value: "1"}},
					},
				},
			},
			servicePort: serviceconfig.Ptr(int32(8080)),
			wantFields:  []string{prefix + "readinessProbe.httpGet.httpHeaders[0].name"},
		},
		{
			name: "metrics endpoint without resolvable port",
			mgmt: &serviceconfig.ManagementConfig{
				Metrics: &serviceconfig.MetricsConfig{Endpoint: "metrics"},
			},
			wantFields: []string{prefix + "metrics.endpoint", prefix + "metrics.port"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Service = nil
			if tc.servicePort != nil {
				cfg.Service = &serviceconfig.ServiceSpec{Port: tc.servicePort}
			}
			cfg.Deployment = &serviceconfig.DeploymentSpec{Management: tc.mgmt}

			if len(tc.wantFields) == 0 {
				if err := Validate(cfg); err != nil {
					t.Errorf("Expected valid config, got: %v", err)
				}
				return
			}
			wantFields(t, cfg, tc.wantFields...)
		})
	}
}

func TestValidate_Gateway(t *testing.T) {
	rule := func(r serviceconfig.HTTPRouteRule) *serviceconfig.GatewayConfig {
		return &serviceconfig.GatewayConfig{
			HTTP: &serviceconfig.GatewayHTTPConfig{
				Enabled: serviceconfig.Ptr(true),
				Rules:   []serviceconfig.HTTPRouteRule{r},
			},
		}
	}

	tests := []struct {
		name       string
		gateway    *serviceconfig.GatewayConfig
		wantFields []string
	}{
		{
			name: "hostname with a scheme",
			gateway: &serviceconfig.GatewayConfig{
				HTTP: &serviceconfig.GatewayHTTPConfig{Hostname: serviceconfig.Ptr("https://example.com")},
			},
			wantFields: []string{"gateway.http.hostname"},
		},
		{
			name: "wildcard hostname is valid",
			gateway: &serviceconfig.GatewayConfig{
				HTTP: &serviceconfig.GatewayHTTPConfig{Hostname: serviceconfig.Ptr("*.example.com")},
			},
		},
		{
			name: "unknown path match type",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Matches: []serviceconfig.HTTPRouteMatch{{
					Path: &serviceconfig.HTTPPathMatch{Type: serviceconfig.Ptr("Prefix"), Value: serviceconfig.Ptr("/api")},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].matches[0].path.type"},
		},
		{
			// An omitted type defaults to PathPrefix, so the value still needs a slash.
			name: "path value without leading slash",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Matches: []serviceconfig.HTTPRouteMatch{{
					Path: &serviceconfig.HTTPPathMatch{Value: serviceconfig.Ptr("api")},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].matches[0].path.value"},
		},
		{
			name: "regex path match skips the slash rule",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Matches: []serviceconfig.HTTPRouteMatch{{
					Path: &serviceconfig.HTTPPathMatch{
						Type:  serviceconfig.Ptr("RegularExpression"),
						Value: serviceconfig.Ptr("^/api/v[0-9]+"),
					},
				}},
			}),
		},
		{
			name: "header match without name or value",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Matches: []serviceconfig.HTTPRouteMatch{{
					Headers: []serviceconfig.HTTPHeaderMatch{{}},
				}},
			}),
			wantFields: []string{
				"gateway.http.rules[0].matches[0].headers[0].name",
				"gateway.http.rules[0].matches[0].headers[0].value",
			},
		},
		{
			name: "lowercase method",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Matches: []serviceconfig.HTTPRouteMatch{{Method: serviceconfig.Ptr("get")}},
			}),
			wantFields: []string{"gateway.http.rules[0].matches[0].method"},
		},
		{
			name: "unknown filter type",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{Type: "Rewrite"}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].type"},
		},
		{
			name: "filter block missing for its type",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{Type: "URLRewrite"}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].urlRewrite"},
		},
		{
			// Gateway API rejects a filter carrying a block from another type.
			name: "filter block belonging to another type",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type:       "RequestRedirect",
					URLRewrite: &serviceconfig.HTTPURLRewriteFilter{Hostname: serviceconfig.Ptr("example.com")},
				}},
			}),
			wantFields: []string{
				"gateway.http.rules[0].filters[0].requestRedirect",
				"gateway.http.rules[0].filters[0].urlRewrite",
			},
		},
		{
			name: "header modifier with nothing to do",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type:                  "RequestHeaderModifier",
					RequestHeaderModifier: &serviceconfig.HTTPHeaderFilter{},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].requestHeaderModifier"},
		},
		{
			name: "header modifier entry without a name",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type: "ResponseHeaderModifier",
					ResponseHeaderModifier: &serviceconfig.HTTPHeaderFilter{
						Set: []serviceconfig.HTTPHeader{{Value: "1"}},
					},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].responseHeaderModifier.set[0].name"},
		},
		{
			name: "redirect with an unsupported scheme and status code",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type: "RequestRedirect",
					RequestRedirect: &serviceconfig.HTTPRequestRedirectFilter{
						Scheme:     serviceconfig.Ptr("HTTPS"),
						StatusCode: serviceconfig.Ptr(308),
					},
				}},
			}),
			wantFields: []string{
				"gateway.http.rules[0].filters[0].requestRedirect.scheme",
				"gateway.http.rules[0].filters[0].requestRedirect.statusCode",
			},
		},
		{
			name: "path modifier missing the field its type names",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type: "URLRewrite",
					URLRewrite: &serviceconfig.HTTPURLRewriteFilter{
						Path: &serviceconfig.HTTPPathModifier{Type: "ReplaceFullPath"},
					},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].urlRewrite.path.replaceFullPath"},
		},
		{
			name: "url rewrite with nothing to rewrite",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type:       "URLRewrite",
					URLRewrite: &serviceconfig.HTTPURLRewriteFilter{},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].urlRewrite"},
		},
		{
			name: "mirror without a backend name",
			gateway: rule(serviceconfig.HTTPRouteRule{
				Filters: []serviceconfig.HTTPRouteFilter{{
					Type:          "RequestMirror",
					RequestMirror: &serviceconfig.HTTPRequestMirrorFilter{},
				}},
			}),
			wantFields: []string{"gateway.http.rules[0].filters[0].requestMirror.backendRef.name"},
		},
		{
			name: "templates are checked once rendered",
			gateway: &serviceconfig.GatewayConfig{HTTP: &serviceconfig.GatewayHTTPConfig{
				Hostname: serviceconfig.Ptr("{{dependencies.dns.host}}"),
				Rules: []serviceconfig.HTTPRouteRule{{
					Matches: []serviceconfig.HTTPRouteMatch{
						{
							Path:    &serviceconfig.HTTPPathMatch{Type: serviceconfig.Ptr("{{variables.TYPE}}"), Value: serviceconfig.Ptr("api")},
							Headers: []serviceconfig.HTTPHeaderMatch{{Type: serviceconfig.Ptr("{{variables.TYPE}}"), Name: "X", Value: "1"}},
							Method:  serviceconfig.Ptr("{{variables.METHOD}}"),
						},
						{Path: &serviceconfig.HTTPPathMatch{Value: serviceconfig.Ptr("{{variables.PREFIX}}")}},
					},
					Filters: []serviceconfig.HTTPRouteFilter{
						{Type: "{{variables.FILTER}}"},
						{Type: "RequestRedirect", RequestRedirect: &serviceconfig.HTTPRequestRedirectFilter{
							Scheme:   serviceconfig.Ptr("{{variables.SCHEME}}"),
							Hostname: serviceconfig.Ptr("{{dependencies.dns.host}}"),
							Path:     &serviceconfig.HTTPPathModifier{Type: "ReplaceFullPath", ReplaceFullPath: serviceconfig.Ptr("{{variables.PATH}}")},
						}},
						{Type: "URLRewrite", URLRewrite: &serviceconfig.HTTPURLRewriteFilter{
							Hostname: serviceconfig.Ptr("{{dependencies.dns.host}}"),
							Path:     &serviceconfig.HTTPPathModifier{Type: "{{variables.TYPE}}", ReplacePrefixMatch: serviceconfig.Ptr("api")},
						}},
					},
				}},
			}},
		},
		{
			name: "plain values next to templates are still checked",
			gateway: &serviceconfig.GatewayConfig{HTTP: &serviceconfig.GatewayHTTPConfig{
				Hostname: serviceconfig.Ptr("{{variables.HOST}}"),
				Rules: []serviceconfig.HTTPRouteRule{{
					Matches: []serviceconfig.HTTPRouteMatch{{Path: &serviceconfig.HTTPPathMatch{Value: serviceconfig.Ptr("api")}}},
				}},
			}},
			wantFields: []string{"gateway.http.rules[0].matches[0].path.value"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Gateway = tc.gateway

			if len(tc.wantFields) == 0 {
				if err := Validate(cfg); err != nil {
					t.Errorf("Expected valid config, got: %v", err)
				}
				return
			}
			wantFields(t, cfg, tc.wantFields...)
		})
	}
}

func TestValidate_EnvConfigsAndDependencies(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*serviceconfig.ServiceConfig)
		wantFields []string
	}{
		{
			name:       "env name with a space",
			mutate:     func(c *serviceconfig.ServiceConfig) { c.Env = map[string]string{"LOG LEVEL": "info"} },
			wantFields: []string{`env["LOG LEVEL"]`},
		},
		{
			name: "config mount path pointing at a directory",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"app": {Format: "yaml", MountPath: "/etc/app/"},
				}
			},
			wantFields: []string{`configs["app"].mountPath`},
		},
		{
			name: "two configs mounted at the same path",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"a": {Format: "yaml", MountPath: "/etc/app/app.yaml"},
					"b": {Format: "yaml", MountPath: "/etc/app/app.yaml"},
				}
			},
			wantFields: []string{`configs["b"].mountPath`},
		},
		{
			name: "unsupported config format and relative mount path",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"app.toml": {Format: "toml", MountPath: "app"},
				}
			},
			wantFields: []string{`configs["app.toml"].format`, `configs["app.toml"].mountPath`},
		},
		{
			name: "config without a format or mount path",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{"app.yaml": {}}
			},
			wantFields: []string{`configs["app.yaml"].format`, `configs["app.yaml"].mountPath`},
		},
		{
			// Templates reference dependencies as {{dependencies.<name>.<output>}},
			// which can't express a dash.
			name: "dependency name outside the template charset",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Dependencies = map[string]serviceconfig.DependencyDeclaration{"my-db": {Type: "postgres"}}
			},
			wantFields: []string{`dependencies["my-db"]`},
		},
		{
			name: "dependency without a type",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Dependencies = map[string]serviceconfig.DependencyDeclaration{"db": {}}
			},
			wantFields: []string{`dependencies["db"].type`},
		},
		{
			// Templates reference variables as {{variables.<name>}}, which can't
			// express a dash or a dot.
			name: "variable names outside the template charset",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Variables = map[string]serviceconfig.VariableDeclaration{
					"SIGNING_KEY": {Secret: true},
					"public_name": {},
					"MY-KEY":      {},
					"my.key":      {},
					"":            {},
				}
			},
			wantFields: []string{`variables[""]`, `variables["MY-KEY"]`, `variables["my.key"]`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(cfg)
			wantFields(t, cfg, tc.wantFields...)
		})
	}
}

func TestValidate_Volumes(t *testing.T) {
	vol := func(mountPath, size string) serviceconfig.VolumeSpec {
		return serviceconfig.VolumeSpec{Type: "shared-single-node", MountPath: mountPath, Size: size}
	}
	one := func(name string, spec serviceconfig.VolumeSpec) func(*serviceconfig.ServiceConfig) {
		return func(c *serviceconfig.ServiceConfig) {
			c.Volumes = map[string]serviceconfig.VolumeSpec{name: spec}
		}
	}

	tests := []struct {
		name       string
		mutate     func(*serviceconfig.ServiceConfig)
		wantFields []string
	}{
		{
			name:   "a single volume is valid",
			mutate: one("data", vol("/data", "1Gi")),
		},
		{
			// Nextcloud splits its web root and its data directory this way.
			name: "a volume inside another volume is valid",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Volumes = map[string]serviceconfig.VolumeSpec{
					"html": vol("/var/www/html", "1Gi"),
					"data": vol("/var/www/html/data", "50Gi"),
				}
			},
		},
		{
			// Home Assistant reads its config file from the directory it keeps state in.
			name: "a config file inside a volume is valid",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"configuration.yaml": {Format: "yaml", MountPath: "/config/configuration.yaml"},
				}
				c.Volumes = map[string]serviceconfig.VolumeSpec{"config": vol("/config", "1Gi")}
			},
		},
		{
			name:   "the longest name that fits the pod volume name",
			mutate: one(strings.Repeat("a", 59), vol("/data", "1Gi")),
		},
		{
			name:       "name too long for the pod volume name",
			mutate:     one(strings.Repeat("a", 60), vol("/data", "1Gi")),
			wantFields: []string{`volumes["` + strings.Repeat("a", 60) + `"]`},
		},
		{
			name:       "name is not a DNS label",
			mutate:     one("my_data", vol("/data", "1Gi")),
			wantFields: []string{`volumes["my_data"]`},
		},
		{
			name:       "empty name",
			mutate:     one("", vol("/data", "1Gi")),
			wantFields: []string{`volumes[""]`},
		},
		{
			// The type states how copies of the service share the volume, so it
			// is never implied.
			name:       "type missing",
			mutate:     one("data", serviceconfig.VolumeSpec{MountPath: "/data", Size: "1Gi"}),
			wantFields: []string{`volumes["data"].type`},
		},
		{
			name:       "type not supported yet",
			mutate:     one("data", serviceconfig.VolumeSpec{Type: "per-replica", MountPath: "/data", Size: "1Gi"}),
			wantFields: []string{`volumes["data"].type`},
		},
		{
			// "shared" alone would hide that the volume is tied to one node.
			name:       "type without its node scope",
			mutate:     one("data", serviceconfig.VolumeSpec{Type: "shared", MountPath: "/data", Size: "1Gi"}),
			wantFields: []string{`volumes["data"].type`},
		},
		{
			name:       "type is case-sensitive",
			mutate:     one("data", serviceconfig.VolumeSpec{Type: "Shared-Single-Node", MountPath: "/data", Size: "1Gi"}),
			wantFields: []string{`volumes["data"].type`},
		},
		{
			name:       "mount path and size missing",
			mutate:     one("data", vol("", "")),
			wantFields: []string{`volumes["data"].mountPath`, `volumes["data"].size`},
		},
		{
			name:       "relative mount path",
			mutate:     one("data", vol("data", "1Gi")),
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name:       "root directory",
			mutate:     one("data", vol("/", "1Gi")),
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name:       "trailing slash",
			mutate:     one("data", vol("/data/", "1Gi")),
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name:       "double slash",
			mutate:     one("data", vol("/var//data", "1Gi")),
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name:       "parent segment",
			mutate:     one("data", vol("/var/lib/../data", "1Gi")),
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name:       "dot segment",
			mutate:     one("data", vol("/var/./data", "1Gi")),
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name: "two volumes at the same path",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Volumes = map[string]serviceconfig.VolumeSpec{
					"a": vol("/data", "1Gi"),
					"b": vol("/data", "1Gi"),
				}
			},
			wantFields: []string{`volumes["b"].mountPath`},
		},
		{
			name: "volume at a config file's path",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"app.yaml": {Format: "yaml", MountPath: "/etc/app.yaml"},
				}
				c.Volumes = map[string]serviceconfig.VolumeSpec{"data": vol("/etc/app.yaml", "1Gi")}
			},
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			name: "volume under a config file's path",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"app.yaml": {Format: "yaml", MountPath: "/etc/app.yaml"},
				}
				c.Volumes = map[string]serviceconfig.VolumeSpec{"data": vol("/etc/app.yaml/data", "1Gi")}
			},
			wantFields: []string{`volumes["data"].mountPath`},
		},
		{
			// A shared prefix is not nesting: /etc/app.yaml.d is a sibling of the file.
			name: "volume beside a config file with a shared prefix",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{
					"app.yaml": {Format: "yaml", MountPath: "/etc/app.yaml"},
				}
				c.Volumes = map[string]serviceconfig.VolumeSpec{"data": vol("/etc/app.yaml.d", "1Gi")}
			},
		},
		{
			// The config's own error is enough; it must not also match every volume.
			name: "config without a mount path does not shadow volumes",
			mutate: func(c *serviceconfig.ServiceConfig) {
				c.Configs = map[string]serviceconfig.ConfigFileSpec{"app.yaml": {Format: "yaml"}}
				c.Volumes = map[string]serviceconfig.VolumeSpec{"data": vol("/data", "1Gi")}
			},
			wantFields: []string{`configs["app.yaml"].mountPath`},
		},
		{
			name:   "size in mebibytes",
			mutate: one("data", vol("/data", "512Mi")),
		},
		{
			name:   "size in tebibytes",
			mutate: one("data", vol("/data", "2Ti")),
		},
		{
			name:   "the largest size that fits in bytes",
			mutate: one("data", vol("/data", "8388607Ti")),
		},
		{
			name:       "size too large to count in bytes",
			mutate:     one("data", vol("/data", "8388608Ti")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "size too large to parse",
			mutate:     one("data", vol("/data", "99999999999999999999Gi")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "decimal unit",
			mutate:     one("data", vol("/data", "10G")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "lowercase unit",
			mutate:     one("data", vol("/data", "10gi")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "kibibytes",
			mutate:     one("data", vol("/data", "1024Ki")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "fraction",
			mutate:     one("data", vol("/data", "1.5Gi")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "zero",
			mutate:     one("data", vol("/data", "0Gi")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "leading zero",
			mutate:     one("data", vol("/data", "010Gi")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			name:       "bare number",
			mutate:     one("data", vol("/data", "10")),
			wantFields: []string{`volumes["data"].size`},
		},
		{
			// Whether copies may write the same files is the app's call; the
			// volumes docs carry the warnings.
			name: "several replicas share the volume",
			mutate: func(c *serviceconfig.ServiceConfig) {
				one("data", vol("/data", "1Gi"))(c)
				c.Deployment = &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(3))}
			},
		},
		{
			name: "zero replicas pauses the service",
			mutate: func(c *serviceconfig.ServiceConfig) {
				one("data", vol("/data", "1Gi"))(c)
				c.Deployment = &serviceconfig.DeploymentSpec{Replicas: serviceconfig.Ptr(int32(0))}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(cfg)

			if len(tc.wantFields) == 0 {
				if err := Validate(cfg); err != nil {
					t.Errorf("Expected valid config, got: %v", err)
				}
				return
			}
			wantFields(t, cfg, tc.wantFields...)
		})
	}
}

func TestVolumeSizeBytes(t *testing.T) {
	tests := map[string]int64{
		"1Mi":    1 << 20,
		"1024Mi": 1 << 30,
		"1Gi":    1 << 30,
		"10Gi":   10 << 30,
		"1Ti":    1 << 40,
	}
	for size, want := range tests {
		got, err := VolumeSizeBytes(size)
		if err != nil || got != want {
			t.Errorf("VolumeSizeBytes(%q) = %d, %v; want %d", size, got, err, want)
		}
	}
}

// Every problem must be reported at once so the user fixes one round of errors
// rather than rediscovering the next one on each submit.
func TestValidate_ReportsAllErrorsAtOnce(t *testing.T) {
	cfg := &serviceconfig.ServiceConfig{Name: "My Service"}

	wantFields(t, cfg, "version", "name", "image")
}

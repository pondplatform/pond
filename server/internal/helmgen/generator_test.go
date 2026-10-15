package helmgen

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

func TestGenerator_Generate_ConfigFiles(t *testing.T) {
	g := NewGenerator()

	// Values are pre-rendered by the deployment service before calling Generate
	cfg := &serviceconfig.ServiceConfig{
		Name: "test-service",
		Configs: map[string]serviceconfig.ConfigFileSpec{
			"app.yaml": {
				Format:    "yaml",
				MountPath: "/etc/app/app.yaml",
				Values: map[string]any{
					"database_url": "postgres://admin:password123@db-host:5432/mydb",
					"debug":        true,
				},
			},
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "example.com",
	}

	vals, err := g.Generate(cfg, env, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(vals.Configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(vals.Configs))
	}

	config := vals.Configs[0]
	if config.MountLocation != "/etc/app/app.yaml" {
		t.Errorf("Unexpected mount location: %s", config.MountLocation)
	}

	decoded, err := base64.StdEncoding.DecodeString(config.Data)
	if err != nil {
		t.Fatalf("Failed to decode config data: %v", err)
	}

	expected := "database_url: postgres://admin:password123@db-host:5432/mydb\ndebug: true\n"
	if string(decoded) != expected {
		t.Errorf("Unexpected rendered config:\nExpected: %q\nGot:      %q", expected, string(decoded))
	}
}

// The chart names config Secrets and pod volumes config-<index>, so the list
// order must not depend on map iteration: a reshuffle changes the pod template.
func TestGenerator_Generate_ConfigFilesInNameOrder(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name: "test-service",
		Configs: map[string]serviceconfig.ConfigFileSpec{
			"c.json":   {Format: "json", MountPath: "/etc/c.json"},
			"a.yaml":   {Format: "yaml", MountPath: "/etc/a.yaml"},
			"b.env":    {Format: ".env", MountPath: "/etc/b.env"},
			"d.yaml":   {Format: "yaml", MountPath: "/etc/d.yaml"},
			"e.config": {Format: "json", MountPath: "/etc/e.config"},
		},
	}

	for range 20 {
		vals, err := g.Generate(cfg, &domain.Environment{}, "", uuid.Nil)
		if err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		var got []string
		for _, c := range vals.Configs {
			got = append(got, c.MountLocation)
		}
		want := []string{"/etc/a.yaml", "/etc/b.env", "/etc/c.json", "/etc/d.yaml", "/etc/e.config"}
		if !slices.Equal(got, want) {
			t.Fatalf("Expected configs in name order %v, got %v", want, got)
		}
	}
}

func TestGenerator_Generate_GatewayHTTPRouteDerivesHostname(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name: "web",
		Gateway: &serviceconfig.GatewayConfig{
			HTTP: &serviceconfig.GatewayHTTPConfig{
				Enabled: serviceconfig.Ptr(true),
			},
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	}

	vals, err := g.Generate(cfg, env, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if !vals.HTTPRoute.Enabled {
		t.Fatal("Expected httpRoute to be enabled")
	}

	if len(vals.HTTPRoute.Hostnames) != 1 || vals.HTTPRoute.Hostnames[0] != "web.pond.local" {
		t.Errorf("Unexpected hostnames: %v", vals.HTTPRoute.Hostnames)
	}

	// With no rules configured, a catch-all prefix route is supplied so the
	// chart's placeholder rule can never survive helm's list replacement.
	if len(vals.HTTPRoute.Rules) != 1 {
		t.Fatalf("Expected 1 defaulted rule, got %d", len(vals.HTTPRoute.Rules))
	}
	matches := vals.HTTPRoute.Rules[0].Matches
	if len(matches) != 1 || matches[0].Path == nil {
		t.Fatalf("Expected a single path match, got %+v", matches)
	}
	if *matches[0].Path.Type != "PathPrefix" || *matches[0].Path.Value != "/" {
		t.Errorf("Unexpected default path match: %+v", *matches[0].Path)
	}
}

func TestGenerator_Generate_GatewayHTTPRouteExplicitHostnameAndRules(t *testing.T) {
	g := NewGenerator()

	rules := []serviceconfig.HTTPRouteRule{{
		Matches: []serviceconfig.HTTPRouteMatch{{
			Path: &serviceconfig.HTTPPathMatch{
				Type:  serviceconfig.Ptr("Exact"),
				Value: serviceconfig.Ptr("/healthz"),
			},
		}},
	}}

	cfg := &serviceconfig.ServiceConfig{
		Name: "web",
		Gateway: &serviceconfig.GatewayConfig{
			HTTP: &serviceconfig.GatewayHTTPConfig{
				Enabled:  serviceconfig.Ptr(true),
				Hostname: serviceconfig.Ptr("api.example.com"),
				Rules:    rules,
			},
		},
	}

	env := &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	}

	vals, err := g.Generate(cfg, env, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if len(vals.HTTPRoute.Hostnames) != 1 || vals.HTTPRoute.Hostnames[0] != "api.example.com" {
		t.Errorf("Explicit hostname should win, got: %v", vals.HTTPRoute.Hostnames)
	}

	if len(vals.HTTPRoute.Rules) != 1 {
		t.Fatalf("Expected the configured rule to pass through, got %d", len(vals.HTTPRoute.Rules))
	}
	if *vals.HTTPRoute.Rules[0].Matches[0].Path.Value != "/healthz" {
		t.Errorf("Unexpected rule: %+v", vals.HTTPRoute.Rules[0])
	}
}

func TestGenerator_Generate_GatewayDisabledByDefault(t *testing.T) {
	g := NewGenerator()

	vals, err := g.Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{
		DefaultIngressBaseHost: "pond.local",
	}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.HTTPRoute.Enabled {
		t.Error("Expected httpRoute to be disabled when gateway is unset")
	}
	if len(vals.HTTPRoute.Rules) != 0 || len(vals.HTTPRoute.Hostnames) != 0 {
		t.Errorf("Expected no rules or hostnames when disabled: %+v", vals.HTTPRoute)
	}
}

func TestGatewayHostname(t *testing.T) {
	gateway := func(enabled *bool, hostname *string) *serviceconfig.GatewayConfig {
		return &serviceconfig.GatewayConfig{HTTP: &serviceconfig.GatewayHTTPConfig{Enabled: enabled, Hostname: hostname}}
	}
	on := serviceconfig.Ptr(true)
	tests := []struct {
		name     string
		gateway  *serviceconfig.GatewayConfig
		env      *domain.Environment
		expected string
	}{
		{"explicit hostname wins", gateway(on, serviceconfig.Ptr("api.example.com")), &domain.Environment{DefaultIngressBaseHost: "pond.local"}, "api.example.com"},
		{"derived from the base host", gateway(on, nil), &domain.Environment{DefaultIngressBaseHost: "pond.local"}, "web.pond.local"},
		{"no base host", gateway(on, nil), &domain.Environment{}, ""},
		{"no environment", gateway(on, nil), nil, ""},
		{"gateway unset", nil, &domain.Environment{DefaultIngressBaseHost: "pond.local"}, ""},
		{"enabled unset", gateway(nil, serviceconfig.Ptr("api.example.com")), &domain.Environment{}, ""},
		{"disabled", gateway(serviceconfig.Ptr(false), serviceconfig.Ptr("api.example.com")), &domain.Environment{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GatewayHostname(&serviceconfig.ServiceConfig{Name: "web", Gateway: tt.gateway}, tt.env)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

// Env and config files reach the pod from Secrets, so only the checksum
// annotation tells Kubernetes the pod template changed.
func TestGenerator_Generate_ConfigChecksum(t *testing.T) {
	checksum := func(env map[string]string, configValue string) string {
		t.Helper()
		cfg := &serviceconfig.ServiceConfig{Name: "web", Env: env}
		if configValue != "" {
			cfg.Configs = map[string]serviceconfig.ConfigFileSpec{
				"a.yaml": {Format: "yaml", MountPath: "/etc/a.yaml", Values: map[string]any{"key": configValue}},
				"b.json": {Format: "json", MountPath: "/etc/b.json", Values: map[string]any{"x": 1, "y": 2}},
			}
		}
		vals, err := NewGenerator().Generate(cfg, &domain.Environment{}, "", uuid.Nil)
		if err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		return vals.PodAnnotations["checksum/config"]
	}

	base := checksum(map[string]string{"A": "1", "B": "2", "C": "3"}, "v1")
	if base == "" {
		t.Fatal("expected a checksum annotation")
	}
	for range 20 {
		if got := checksum(map[string]string{"C": "3", "B": "2", "A": "1"}, "v1"); got != base {
			t.Fatalf("expected a stable checksum %s, got %s", base, got)
		}
	}
	if checksum(map[string]string{"A": "1", "B": "2", "C": "changed"}, "v1") == base {
		t.Error("expected an env change to change the checksum")
	}
	if checksum(map[string]string{"A": "1", "B": "2", "C": "3"}, "v2") == base {
		t.Error("expected a config file change to change the checksum")
	}
	if checksum(nil, "v1") == "" || checksum(map[string]string{"A": "1"}, "") == "" {
		t.Error("expected a checksum with only config files or only env")
	}
	if got := checksum(nil, ""); got != "" {
		t.Errorf("expected no checksum without env or config files, got %s", got)
	}
}

// A redeploy of an unchanged config must still roll the pods, so each
// deployment's ID alone has to change the pod template.
func TestGenerator_Generate_DeploymentIDAnnotation(t *testing.T) {
	annotation := func(id uuid.UUID) string {
		t.Helper()
		vals, err := NewGenerator().Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{}, "", id)
		if err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		return vals.PodAnnotations["pond/deployment-id"]
	}

	first, second := uuid.New(), uuid.New()
	if got := annotation(first); got != first.String() {
		t.Errorf("expected the deployment ID %s, got %q", first, got)
	}
	if annotation(first) == annotation(second) {
		t.Error("expected another deployment to change the annotation")
	}
}

// Regression guard: an unset replica count must be omitted from the values so
// the chart default of 1 applies, rather than pinning the deployment to zero.
func TestGenerator_Generate_OmitsUnsetReplicaCountAndPort(t *testing.T) {
	g := NewGenerator()

	vals, err := g.Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.ReplicaCount != nil {
		t.Errorf("Expected replicaCount to be omitted, got %d", *vals.ReplicaCount)
	}
	if vals.Service.Port != nil {
		t.Errorf("Expected service port to be omitted, got %d", *vals.Service.Port)
	}

	out, err := yaml.Marshal(vals)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(out), "replicaCount") {
		t.Errorf("replicaCount should not appear in the values file:\n%s", out)
	}
}

func TestGenerator_Generate_ReplicasFromDeployment(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name: "web",
		Deployment: &serviceconfig.DeploymentSpec{
			Replicas: serviceconfig.Ptr(int32(3)),
		},
	}

	vals, err := g.Generate(cfg, &domain.Environment{}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if vals.ReplicaCount == nil || *vals.ReplicaCount != 3 {
		t.Errorf("Unexpected replicaCount: %v", vals.ReplicaCount)
	}
}

func TestGenerator_Generate_Volumes(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name: "files",
		Volumes: map[string]serviceconfig.VolumeSpec{
			"uploads": {Type: "shared-single-node", MountPath: "/var/www/html/data", Size: "50Gi"},
			"html":    {Type: "shared-single-node", MountPath: "/var/www/html", Size: "1Gi"},
		},
	}

	vals, err := g.Generate(cfg, &domain.Environment{}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// A ReadWriteOnce claim can't be mounted by two pods, so the old pod stops first.
	if vals.Strategy.Type != "Recreate" {
		t.Errorf("Expected Recreate with volumes, got %q", vals.Strategy.Type)
	}

	wantClaims := []HelmPersistentVolumeClaim{{Name: "html", Size: "1Gi"}, {Name: "uploads", Size: "50Gi"}}
	if !slices.Equal(vals.PersistentVolumeClaims, wantClaims) {
		t.Errorf("claims: want %+v, got %+v", wantClaims, vals.PersistentVolumeClaims)
	}
	// The chart names each claim <fullname>-<name>, and fullname is the service name.
	wantVolumes := []HelmVolume{
		{Name: "vol-html", PersistentVolumeClaim: HelmPersistentVolumeClaimRef{ClaimName: "files-html"}},
		{Name: "vol-uploads", PersistentVolumeClaim: HelmPersistentVolumeClaimRef{ClaimName: "files-uploads"}},
	}
	if !slices.Equal(vals.Volumes, wantVolumes) {
		t.Errorf("volumes: want %+v, got %+v", wantVolumes, vals.Volumes)
	}
	wantMounts := []HelmVolumeMount{
		{Name: "vol-html", MountPath: "/var/www/html"},
		{Name: "vol-uploads", MountPath: "/var/www/html/data"},
	}
	if !slices.Equal(vals.VolumeMounts, wantMounts) {
		t.Errorf("mounts: want %+v, got %+v", wantMounts, vals.VolumeMounts)
	}

	sc := vals.PodSecurityContext
	if sc == nil || sc.FSGroup == nil || *sc.FSGroup != 1000 || sc.FSGroupChangePolicy != "OnRootMismatch" {
		t.Errorf("Expected fsGroup 1000 with OnRootMismatch, got %+v", sc)
	}
}

// Without volumes the values must not carry any of the volume keys: the chart
// defaults apply, and the pod template stays what it was before volumes existed.
func TestGenerator_Generate_NoVolumes(t *testing.T) {
	g := NewGenerator()

	vals, err := g.Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	out, err := yaml.Marshal(vals)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	for _, key := range []string{"persistentVolumeClaims", "podSecurityContext", "fsGroup", "volumes", "volumeMounts"} {
		if strings.Contains(string(out), key+":") {
			t.Errorf("%s should not appear in the values file:\n%s", key, out)
		}
	}
	// The strategy is always explicit, so helm owns rollingUpdate and a later
	// switch to Recreate can remove it.
	if !strings.Contains(string(out), "strategy:\n    type: RollingUpdate\n") {
		t.Errorf("Expected an explicit RollingUpdate strategy:\n%s", out)
	}
}

func TestGenerator_Generate_Command(t *testing.T) {
	g := NewGenerator()

	cfg := &serviceconfig.ServiceConfig{
		Name:    "worker",
		Command: []string{"sh", "-c", `echo "pid $$, home $HOME, $(SECRET)" && exec worker`, "$"},
	}
	vals, err := g.Generate(cfg, &domain.Environment{}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Kubernetes would expand $(SECRET) from the container's env and reduce $$
	// to $; doubled, each $ reaches the shell as written.
	want := []string{"sh", "-c", `echo "pid $$$$, home $$HOME, $$(SECRET)" && exec worker`, "$$"}
	if !slices.Equal(vals.Args, want) {
		t.Errorf("args: want %q, got %q", want, vals.Args)
	}
	if cfg.Command[3] != "$" {
		t.Errorf("the config's command was modified: %q", cfg.Command)
	}
}

// Without a command the chart renders no args, so the image's CMD runs.
func TestGenerator_Generate_NoCommand(t *testing.T) {
	vals, err := NewGenerator().Generate(&serviceconfig.ServiceConfig{Name: "web"}, &domain.Environment{}, "", uuid.Nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	out, err := yaml.Marshal(vals)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(out), "args:") {
		t.Errorf("args should not appear in the values file:\n%s", out)
	}
}

func TestGenerator_Generate_ImageTag(t *testing.T) {
	g := NewGenerator()

	tests := []struct {
		name     string
		image    string
		imageTag string
		wantRepo string
		wantTag  string
	}{
		// The submitted tag is what the user asked to deploy, so it wins even when
		// pond.yml pins one — otherwise `--tag` would silently deploy something else.
		{"submitted tag wins over a pinned one", "ghcr.io/acme/api:0.0.9", "0.0.1", "ghcr.io/acme/api", "0.0.1"},
		{"submitted tag fills in an untagged image", "ghcr.io/acme/api", "0.0.1", "ghcr.io/acme/api", "0.0.1"},
		{"pinned tag applies when none is submitted", "ghcr.io/acme/api:0.0.9", "", "ghcr.io/acme/api", "0.0.9"},
		{"latest is the last resort", "ghcr.io/acme/api", "", "ghcr.io/acme/api", "latest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vals, err := g.Generate(
				&serviceconfig.ServiceConfig{Name: "api", Image: tt.image},
				&domain.Environment{},
				tt.imageTag,
				uuid.Nil,
			)
			if err != nil {
				t.Fatalf("Generate failed: %v", err)
			}
			if vals.Image.Repository != tt.wantRepo {
				t.Errorf("repository: want %q, got %q", tt.wantRepo, vals.Image.Repository)
			}
			if vals.Image.Tag != tt.wantTag {
				t.Errorf("tag: want %q, got %q", tt.wantTag, vals.Image.Tag)
			}
		})
	}
}

func Test_Marshal_Dotenv(t *testing.T) {
	values := map[string]any{
		"PASSWORD": `p$ss"w\rd #1`,
		"PORT":     5432,
		"QUOTE":    `it's $HOME`,
		"CERT":     "line1\nline2",
		"EMPTY":    "",
	}
	marshaled, err := marshalEnv(values)
	if err != nil {
		t.Fatalf("marshalEnv failed: %v", err)
	}

	expected := `CERT="line1\nline2"
EMPTY=''
PASSWORD='p$ss"w\rd #1'
PORT='5432'
QUOTE="it's $HOME"
`
	if string(marshaled) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, marshaled)
	}
}

package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

func TestParse_RejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "top level",
			yaml: "version: 1\nname: api\nimgae: ghcr.io/acme/api\n",
			want: `pond.yml:3: unknown field "imgae"`,
		},
		{
			name: "nested",
			yaml: "version: 1\nname: api\ndeployment:\n  replica: 2\n",
			want: `pond.yml:4: unknown field "replica"`,
		},
		{
			name: "inside an override",
			yaml: "version: 1\nname: api\noverrides:\n  production:\n    gateway:\n      http:\n        hostnme: api.example.com\n",
			want: `pond.yml:7: unknown field "hostnme"`,
		},
		{
			name: "base-only key in an override",
			yaml: "version: 1\nname: api\noverrides:\n  production:\n    image: ghcr.io/acme/api\n",
			want: `pond.yml:5: unknown field "image"`,
		},
		{
			// A service needs the same storage in every environment.
			name: "volumes in an override",
			yaml: "version: 1\nname: api\noverrides:\n  production:\n    volumes:\n      data:\n        type: shared-single-node\n        mountPath: /data\n        size: 1Gi\n",
			want: `pond.yml:5: unknown field "volumes"`,
		},
		{
			name: "inside a volume",
			yaml: "version: 1\nname: api\nvolumes:\n  data:\n    type: shared-single-node\n    path: /data\n    size: 1Gi\n",
			want: `pond.yml:6: unknown field "path"`,
		},
		{
			// Splitting a string into arguments would need shell quoting rules.
			name: "command as a string",
			yaml: "version: 1\nname: api\ncommand: serve --port 8080\n",
			want: "pond.yml:3: cannot unmarshal !!str `serve -...` into []string",
		},
		{
			name: "every problem is reported",
			yaml: "version: 1\nname: api\nimgae: x\nservice:\n  prot: 80\n",
			want: `pond.yml:3: unknown field "imgae"; pond.yml:5: unknown field "prot"`,
		},
		{
			name: "type errors keep their line",
			yaml: "version: 1\nname: api\nservice:\n  port: http\n",
			want: "pond.yml:4: cannot unmarshal !!str `http` into int32",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.NewParser().Parse(strings.NewReader(tt.yaml))
			if err == nil || err.Error() != tt.want {
				t.Errorf("got %v\nwant %s", err, tt.want)
			}
		})
	}
}

func TestParse_AcceptsFreeFormMaps(t *testing.T) {
	yaml := `version: 1
name: api
env:
  ANY_NAME: value
configs:
  app:
    format: json
    mountPath: /etc/app/config.json
    values:
      anything: goes
      nested: {deep: true}
dependencies:
  db:
    type: postgres
    config:
      whatever: 1
`
	cfg, err := config.NewParser().Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.Configs["app"].Values["anything"] != "goes" || cfg.Dependencies["db"].Config["whatever"] != 1 {
		t.Errorf("free-form values not decoded: %+v", cfg)
	}
}

func TestParse_Volumes(t *testing.T) {
	yaml := `version: 1
name: api
volumes:
  data:
    type: shared-single-node
    mountPath: /data
    size: 10Gi
`
	cfg, err := config.NewParser().Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	want := serviceconfig.VolumeSpec{Type: "shared-single-node", MountPath: "/data", Size: "10Gi"}
	if got := cfg.Volumes["data"]; got != want {
		t.Errorf("want %+v, got %+v", want, got)
	}
}

func TestParse_Command(t *testing.T) {
	yaml := `version: 1
name: api
command: [serve, --port, 8080, --verbose, true, "{{service.port}}"]
overrides:
  staging:
    command: [serve, --debug]
`
	cfg, err := config.NewParser().Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	// Unquoted numbers and booleans are arguments like any other.
	if want := []string{"serve", "--port", "8080", "--verbose", "true", "{{service.port}}"}; !slices.Equal(cfg.Command, want) {
		t.Errorf("want %q, got %q", want, cfg.Command)
	}
	if want := []string{"serve", "--debug"}; !slices.Equal(cfg.Overrides["staging"].Command, want) {
		t.Errorf("override: want %q, got %q", want, cfg.Overrides["staging"].Command)
	}
}

// The CLI sends the parsed file as JSON, and an empty command is only
// rejected if the server can still tell it apart from none.
func TestParse_EmptyCommandSurvivesTheWire(t *testing.T) {
	sent := func(yaml string) serviceconfig.OverridableConfig {
		t.Helper()
		cfg, err := config.NewParser().Parse(strings.NewReader(yaml))
		if err != nil {
			t.Fatalf("Parse failed: %v", err)
		}
		body, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		var received serviceconfig.OverridableConfig
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("Unmarshal failed: %v", err)
		}
		return received
	}

	empty := sent("version: 1\nname: api\ncommand: []\noverrides:\n  staging:\n    command: []\n")
	if empty.Command == nil || empty.Overrides["staging"].Command == nil {
		t.Errorf("an empty command arrived as unset: base %#v, override %#v", empty.Command, empty.Overrides["staging"].Command)
	}
	unset := sent("version: 1\nname: api\ncommand:\noverrides:\n  staging:\n    env: {}\n")
	if unset.Command != nil || unset.Overrides["staging"].Command != nil {
		t.Errorf("an unset command arrived as set: base %#v, override %#v", unset.Command, unset.Overrides["staging"].Command)
	}
}

func TestParseFile_ReportsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pond.yml")
	if err := os.WriteFile(path, []byte("version: 1\nnmae: api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.NewParser().ParseFile(path)
	want := path + `:2: unknown field "nmae"`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

package config_test

import (
	"os"
	"path/filepath"
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

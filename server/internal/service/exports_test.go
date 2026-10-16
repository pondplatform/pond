package service

import (
	"maps"
	"testing"

	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

func TestComputeExports(t *testing.T) {
	env := &db.Environment{Namespace: "e2e-staging", DefaultIngressBaseHost: "e2e.local", GatewayTLS: true}
	tests := map[string]struct {
		cfg       serviceconfig.ServiceConfig
		variables map[string]string
		want      map[string]string
	}{
		"no port and no gateway": {
			cfg: serviceconfig.ServiceConfig{Name: "api"},
			want: map[string]string{
				"host": "api.e2e-staging.svc.cluster.local",
				"port": "80",
				"url":  "http://api.e2e-staging.svc.cluster.local:80",
			},
		},
		"port and a gateway": {
			cfg: serviceconfig.ServiceConfig{
				Name:    "api",
				Service: &serviceconfig.ServiceSpec{Port: serviceconfig.Ptr(int32(8080))},
				Gateway: gateway(nil),
			},
			want: map[string]string{
				"host":             "api.e2e-staging.svc.cluster.local",
				"port":             "8080",
				"url":              "http://api.e2e-staging.svc.cluster.local:8080",
				"gateway.hostname": "api.e2e.local",
				"gateway.port":     "443",
				"gateway.url":      "https://api.e2e.local",
			},
		},
		"only public variables": {
			cfg: serviceconfig.ServiceConfig{Name: "api", Variables: map[string]serviceconfig.VariableDeclaration{
				"API_KEY":  {Secret: true, Visibility: "public"},
				"REGION":   {Visibility: "public"},
				"PASSWORD": {Secret: true},
				"OWN":      {Visibility: "private"},
			}},
			variables: map[string]string{"API_KEY": "k3y", "REGION": "eu", "PASSWORD": "s3cr3t", "OWN": "mine"},
			want: map[string]string{
				"host":              "api.e2e-staging.svc.cluster.local",
				"port":              "80",
				"url":               "http://api.e2e-staging.svc.cluster.local:80",
				"variables.API_KEY": "k3y",
				"variables.REGION":  "eu",
			},
		},
		"wildcard gateway": {
			cfg: serviceconfig.ServiceConfig{Name: "api", Gateway: gateway(serviceconfig.Ptr("*.e2e.local"))},
			want: map[string]string{
				"host": "api.e2e-staging.svc.cluster.local",
				"port": "80",
				"url":  "http://api.e2e-staging.svc.cluster.local:80",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := computeExports(&tc.cfg, env, tc.variables); !maps.Equal(got, tc.want) {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

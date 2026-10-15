package api

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	shared "github.com/pondplatform/pond/shared/server/api"
)

func TestDecodeJSON_Messages(t *testing.T) {
	type nested struct {
		Replicas int32 `json:"replicas"`
	}
	type target struct {
		Name       string    `json:"name"`
		ProjectID  uuid.UUID `json:"projectId"`
		Enabled    bool      `json:"enabled"`
		Deployment nested    `json:"deployment"`
	}
	tests := []struct {
		body string
		want string
	}{
		{"", "request body is empty; expected a JSON object"},
		{`{"name":`, "request body is not valid JSON: unexpected end of input"},
		{`{name}`, "request body is not valid JSON: invalid character 'n' looking for beginning of object key string (at byte 2)"},
		{`[]`, "request body must be a JSON object, got array"},
		{`{"name": 5}`, `field "name" must be a string, got number`},
		{`{"projectId": 5}`, `field "projectId" must be a UUID string, got number`},
		{`{"enabled": "yes"}`, `field "enabled" must be a boolean, got string`},
		{`{"deployment": {"replicas": "3"}}`, `field "deployment.replicas" must be an integer, got string`},
		{`{"project": "x"}`, `unknown field "project" in request body; check the spelling, or whether the client and server versions differ`},
		{`{"projectId": "abc"}`, "invalid request body: invalid UUID length: 3"},
	}
	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			var v target
			err := decodeJSON(strings.NewReader(tt.body), &v)
			if err == nil || err.Error() != tt.want {
				t.Errorf("got %v\nwant %s", err, tt.want)
			}
		})
	}
}

func TestDecodeJSON_SubmitRequestRejectsUnknownConfigFields(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{`{"overridableConfig": {"imgae": "x"}}`, `unknown field "imgae"`},
		{`{"overridableConfig": {"deployment": {"replica": 2}}}`, `unknown field "replica"`},
		{`{"overridableConfig": {"overrides": {"production": {"image": "x"}}}}`, `unknown field "image"`},
		{`{"overridableConfig": {"gateway": {"http": {"rules": [{"matches": [{"pth": {}}]}]}}}}`, `unknown field "pth"`},
	}
	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			var req shared.SubmitRequest
			err := decodeJSON(strings.NewReader(tt.body), &req)
			if err == nil || !strings.HasPrefix(err.Error(), tt.want+" in request body") {
				t.Errorf("got %v\nwant prefix %s", err, tt.want)
			}
		})
	}
}

func TestDecodeJSON_SubmitRequestCarriesTheInput(t *testing.T) {
	body := `{
		"overridableConfig": {"variables": {"SIGNING_KEY": {"secret": true}, "PUBLIC_NAME": {}}},
		"dependencies": {"db": {"managed": true}},
		"variables": {"SIGNING_KEY": "s3cr3t", "PUBLIC_NAME": "Acme"}
	}`
	var req shared.SubmitRequest
	if err := decodeJSON(strings.NewReader(body), &req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !req.Dependencies["db"].Managed || req.Variables["SIGNING_KEY"] != "s3cr3t" || req.Variables["PUBLIC_NAME"] != "Acme" {
		t.Errorf("input not decoded: %+v", req.DeploymentInput)
	}
	declared := req.OverridableConfig.Variables
	if len(declared) != 2 || !declared["SIGNING_KEY"].Secret || declared["PUBLIC_NAME"].Secret {
		t.Errorf("variable declarations not decoded: %+v", declared)
	}
}

func TestDecodeJSON_Valid(t *testing.T) {
	var v struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(strings.NewReader(`{"name":"x"}`), &v); err != nil || v.Name != "x" {
		t.Fatalf("err=%v v=%+v", err, v)
	}
}

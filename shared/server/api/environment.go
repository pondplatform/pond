package api

import (
	"time"

	"github.com/google/uuid"
)

func (r CreateEnvironmentRequest) Validate() error {
	var errs ValidationErrors
	if r.Name == "" {
		errs.Add("Environment", "name", "must not be empty")
	}
	if r.Namespace == "" {
		errs.Add("Environment", "namespace", "must not be empty")
	}
	if r.ClusterID == uuid.Nil {
		errs.Add("Environment", "clusterId", "must not be empty")
	}
	if r.GatewayPort != nil && (*r.GatewayPort < 1 || *r.GatewayPort > 65535) {
		errs.Add("Environment", "gatewayPort", "must be between 1 and 65535")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

func (r UpdateEnvironmentRequest) Validate() error {
	var errs ValidationErrors
	if r.Namespace != nil && *r.Namespace == "" {
		errs.Add("Environment", "namespace", "must not be empty")
	}
	if r.ClusterID != nil && *r.ClusterID == uuid.Nil {
		errs.Add("Environment", "clusterId", "must not be empty")
	}
	if r.GatewayPort != nil && (*r.GatewayPort < 0 || *r.GatewayPort > 65535) {
		errs.Add("Environment", "gatewayPort", "must be between 1 and 65535, or 0 to use the default")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

type CreateEnvironmentRequest struct {
	Name                   string     `json:"name"`
	Namespace              string     `json:"namespace"`
	ClusterID              uuid.UUID  `json:"clusterId"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	DefaultIngressBaseHost string     `json:"defaultIngressBaseHost"`
	// GatewayPort defaults to 443 with TLS and 80 without.
	GatewayPort *int32 `json:"gatewayPort"`
	// GatewayTLS defaults to true.
	GatewayTLS *bool `json:"gatewayTls"`
}

type UpdateEnvironmentRequest struct {
	ClusterID              *uuid.UUID `json:"clusterId"`
	Namespace              *string    `json:"namespace"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	DefaultIngressBaseHost *string    `json:"defaultIngressBaseHost"`
	// GatewayPort 0 resets the port to the default of the scheme.
	GatewayPort *int32 `json:"gatewayPort"`
	GatewayTLS  *bool  `json:"gatewayTls"`
}

type Environment struct {
	ID                     uuid.UUID  `json:"id"`
	ProjectID              uuid.UUID  `json:"projectId"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	Name                   string     `json:"name"`
	Namespace              string     `json:"namespace"`
	DefaultIngressBaseHost string     `json:"defaultIngressBaseHost"`
	// GatewayPort is nil when the gateway listens on the default of its scheme.
	GatewayPort *int32    `json:"gatewayPort"`
	GatewayTLS  bool      `json:"gatewayTls"`
	ClusterID   uuid.UUID `json:"clusterId"`
	CreatedAt   time.Time `json:"createdAt"`
}

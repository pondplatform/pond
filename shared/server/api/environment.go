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
}

type UpdateEnvironmentRequest struct {
	ClusterID              *uuid.UUID `json:"clusterId"`
	Namespace              *string    `json:"namespace"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	DefaultIngressBaseHost *string    `json:"defaultIngressBaseHost"`
}

type Environment struct {
	ID                     uuid.UUID  `json:"id"`
	ProjectID              uuid.UUID  `json:"projectId"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	Name                   string     `json:"name"`
	Namespace              string     `json:"namespace"`
	DefaultIngressBaseHost string     `json:"defaultIngressBaseHost"`
	ClusterID              uuid.UUID  `json:"clusterId"`
	CreatedAt              time.Time  `json:"createdAt"`
}

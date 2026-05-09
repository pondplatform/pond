package domain

import (
	"time"

	"github.com/google/uuid"
)

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

func (e Environment) Validate() error {
	var errs ValidationErrors
	if e.Name == "" {
		errs.Add("Environment", "name", "must not be empty")
	}
	if e.Namespace == "" {
		errs.Add("Environment", "namespace", "must not be empty")
	}
	if e.ClusterID == uuid.Nil {
		errs.Add("Environment", "clusterId", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}


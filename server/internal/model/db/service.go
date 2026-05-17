package db

import (
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/server/api"
)

type Service struct {
	ID                  uuid.UUID  `json:"id"`
	ProjectID           uuid.UUID  `json:"projectId"`
	Name                string     `json:"name"`
	CurrentDeploymentID *uuid.UUID `json:"currentDeploymentId"`
	RunningDeploymentID *uuid.UUID `json:"runningDeploymentId"`
	CreatedAt           time.Time  `json:"createdAt"`
}

func (s Service) Validate() error {
	var errs api.ValidationErrors
	if s.Name == "" {
		errs.Add("Service", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

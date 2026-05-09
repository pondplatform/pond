package domain

import (
	"time"

	"github.com/google/uuid"
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
	var errs ValidationErrors
	if s.Name == "" {
		errs.Add("Service", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}


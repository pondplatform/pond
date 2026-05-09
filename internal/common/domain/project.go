package domain

import (
	"time"

	"github.com/google/uuid"
)

type Project struct {
	ID                uuid.UUID  `json:"id"`
	OrganizationID    uuid.UUID  `json:"organizationId"`
	Name              string     `json:"name"`
	RootEnvironmentID *uuid.UUID `json:"rootEnvironmentId"`
	CreatedAt         time.Time  `json:"createdAt"`
}

func (p Project) Validate() error {
	var errs ValidationErrors
	if p.Name == "" {
		errs.Add("Project", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}


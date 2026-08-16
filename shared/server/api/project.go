package api

import (
	"time"

	"github.com/google/uuid"
)

type CreateProjectRequest struct {
	Name string `json:"name"`
}

func (r CreateProjectRequest) Validate() error {
	var errs ValidationErrors
	if r.Name == "" {
		errs.Add("Project", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

type UpdateProjectRequest struct {
	RootEnvironmentID *uuid.UUID `json:"rootEnvironmentId"`
}

type Project struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	RootEnvironmentID *uuid.UUID `json:"rootEnvironmentId"`
	CreatedAt         time.Time  `json:"createdAt"`
}

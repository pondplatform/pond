package api

import (
	"time"

	"github.com/google/uuid"
)

type CreateOrganizationRequest struct {
	Name string `json:"name"`
}

func (r CreateOrganizationRequest) Validate() error {
	var errs ValidationErrors
	if r.Name == "" {
		errs.Add("Organization", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

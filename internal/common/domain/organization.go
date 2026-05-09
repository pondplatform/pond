package domain

import (
	"time"

	"github.com/google/uuid"
)

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func (o Organization) Validate() error {
	var errs ValidationErrors
	if o.Name == "" {
		errs.Add("Organization", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}


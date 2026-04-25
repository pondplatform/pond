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


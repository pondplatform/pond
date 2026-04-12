package domain

import (
	"time"

	"github.com/google/uuid"
)

type Project struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	Name              string
	RootEnvironmentID *uuid.UUID
	CreatedAt         time.Time
}


package domain

import (
	"time"

	"github.com/google/uuid"
)

type Service struct {
	ID                  uuid.UUID
	ProjectID           uuid.UUID
	Name                string
	CurrentDeploymentID *uuid.UUID
	RunningDeploymentID *uuid.UUID
	CreatedAt           time.Time
}


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


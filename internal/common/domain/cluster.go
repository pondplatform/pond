package domain

import (
	"time"

	"github.com/google/uuid"
)

type Cluster struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	AgentTokenHash string
	LastSeenAt     *time.Time
	CreatedAt      time.Time
}


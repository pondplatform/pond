package domain

import (
	"time"

	"github.com/google/uuid"
)

type Cluster struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organizationId"`
	Name           string     `json:"name"`
	AgentTokenHash string     `json:"agentTokenHash"`
	LastSeenAt     *time.Time `json:"lastSeenAt"`
	CreatedAt      time.Time  `json:"createdAt"`
}


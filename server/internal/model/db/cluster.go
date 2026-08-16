package db

import (
	"time"

	"github.com/google/uuid"
)

type Cluster struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	AgentTokenHash string     `json:"agentTokenHash"`
	LastSeenAt     *time.Time `json:"lastSeenAt"`
	CreatedAt      time.Time  `json:"createdAt"`
}

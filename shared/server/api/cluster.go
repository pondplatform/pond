package api

import (
	"time"

	"github.com/google/uuid"
)

type CreateClusterRequest struct {
	Name string `json:"name"`
}

func (r CreateClusterRequest) Validate() error {
	var errs ValidationErrors
	if r.Name == "" {
		errs.Add("Cluster", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

type Cluster struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	AgentToken string     `json:"agentToken,omitempty"`
	LastSeenAt *time.Time `json:"lastSeenAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type RotateTokenResponse struct {
	AgentToken string `json:"agentToken"`
}

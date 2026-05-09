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

func (c Cluster) Validate() error {
	var errs ValidationErrors
	if c.Name == "" {
		errs.Add("Cluster", "name", "must not be empty")
	}
	if c.AgentTokenHash == "" {
		errs.Add("Cluster", "agentTokenHash", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}


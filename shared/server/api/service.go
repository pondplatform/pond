package api

import (
	"time"

	"github.com/google/uuid"
)

// ServiceStatus is derived from the latest deployment of the service in each
// environment it was deployed to.
type ServiceStatus string

const (
	// ServiceStatusActive: the service runs, or is being deployed, in at least
	// one environment, or a delete of it failed somewhere.
	ServiceStatusActive ServiceStatus = "active"
	// ServiceStatusDeleting: a delete is in progress in some environment.
	ServiceStatusDeleting ServiceStatus = "deleting"
	// ServiceStatusDeleted: the service was deleted from every environment.
	// Deploying it again makes it active.
	ServiceStatusDeleted ServiceStatus = "deleted"
)

type Service struct {
	ID                  uuid.UUID     `json:"id"`
	ProjectID           uuid.UUID     `json:"projectId"`
	Name                string        `json:"name"`
	CurrentDeploymentID *uuid.UUID    `json:"currentDeploymentId"`
	RunningDeploymentID *uuid.UUID    `json:"runningDeploymentId"`
	Status              ServiceStatus `json:"status"`
	CreatedAt           time.Time     `json:"createdAt"`
}

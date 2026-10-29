package db

import (
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/shared/server/api"
)

type Service struct {
	ID                  uuid.UUID  `json:"id"`
	ProjectID           uuid.UUID  `json:"projectId"`
	Name                string     `json:"name"`
	CurrentDeploymentID *uuid.UUID `json:"currentDeploymentId"`
	RunningDeploymentID *uuid.UUID `json:"runningDeploymentId"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// ServiceStatus derives a service's status from latest, its latest deployment
// in each environment it was deployed to: deleting while one of them is a
// delete in progress, deleted once all of them are torn down.
func ServiceStatus(latest []Deployment) api.ServiceStatus {
	if len(latest) == 0 {
		return api.ServiceStatusActive
	}
	tornDown := true
	for _, d := range latest {
		if d.Kind == DeploymentKindDelete && d.Status == DeploymentInProgress {
			return api.ServiceStatusDeleting
		}
		tornDown = tornDown && d.TornDown()
	}
	if tornDown {
		return api.ServiceStatusDeleted
	}
	return api.ServiceStatusActive
}

func (s Service) Validate() error {
	var errs api.ValidationErrors
	if s.Name == "" {
		errs.Add("Service", "name", "must not be empty")
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

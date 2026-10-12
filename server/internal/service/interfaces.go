package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type DeploymentDetail struct {
	Deployment   *db.Deployment
	Dependencies []db.DependencyDeployment
	Commands     []*db.Command
}

type DeploymentService interface {
	Submit(ctx context.Context, req api.SubmitRequest) (*db.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*DeploymentDetail, error)
	ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]db.Deployment, error)
	Cancel(ctx context.Context, deploymentID uuid.UUID) error
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]db.CommandLog, error)
}

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
)

func newCommand(clusterID, deploymentID uuid.UUID, commandType string, payload []byte) *db.Command {
	now := time.Now()
	return &db.Command{
		ID:           uuid.New(),
		ClusterID:    clusterID,
		DeploymentID: deploymentID,
		Type:         commandType,
		Payload:      payload,
		Status:       db.CommandStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func notifyCommandQueued(ctx context.Context, topics events.Topics, cmd *db.Command) error {
	if err := topics.CommandQueuedPublisher(cmd.ClusterID).Publish(ctx, events.CommandQueued{
		ClusterID:    cmd.ClusterID,
		CommandID:    cmd.ID,
		DeploymentID: cmd.DeploymentID,
	}); err != nil {
		return fmt.Errorf("notify cluster %s of queued command: %w", cmd.ClusterID, err)
	}
	return nil
}

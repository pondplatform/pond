package service

import (
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
)

func newCommand(clusterID, deploymentID uuid.UUID, commandType string, phase int, payload []byte) *db.Command {
	now := time.Now()
	return &db.Command{
		ID:           uuid.New(),
		ClusterID:    clusterID,
		DeploymentID: deploymentID,
		Type:         commandType,
		Phase:        phase,
		Payload:      payload,
		Status:       db.CommandStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

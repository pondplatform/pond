package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/deployment"
)

type commandQueue struct {
	db *sql.DB
}

func NewCommandQueue(db *sql.DB) deployment.CommandQueue {
	return &commandQueue{db: db}
}

func (q *commandQueue) Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *deployment.Command) error {
	payload, err := json.Marshal(cmd.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = q.db.ExecContext(ctx,
		"INSERT INTO command_queue (id, cluster_id, deployment_id, type, payload, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		cmd.ID, clusterID, cmd.DeploymentID, cmd.Type, payload, cmd.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue command: %w", err)
	}
	return nil
}

func (q *commandQueue) Dequeue(ctx context.Context, clusterID uuid.UUID) (*deployment.Command, error) {
	var cmd deployment.Command
	var payload []byte
	err := q.db.QueryRowContext(ctx,
		"DELETE FROM command_queue WHERE id = (SELECT id FROM command_queue WHERE cluster_id = $1 ORDER BY created_at ASC LIMIT 1) RETURNING id, deployment_id, type, payload, created_at",
		clusterID,
	).Scan(&cmd.ID, &cmd.DeploymentID, &cmd.Type, &payload, &cmd.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeue command: %w", err)
	}
	cmd.Payload = payload
	return &cmd, nil
}

func (q *commandQueue) Acknowledge(ctx context.Context, cmdID uuid.UUID, result *deployment.CommandResult) error {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	_, err = q.db.ExecContext(ctx,
		"INSERT INTO command_results (command_id, success, output, error, completed_at) VALUES ($1, $2, $3, $4, $5)",
		cmdID, result.Success, resultJSON, result.Error, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("acknowledge command: %w", err)
	}
	return nil
}

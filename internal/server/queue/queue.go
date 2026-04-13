package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/server/service"
)

type commandQueue struct {
	db *sql.DB
}

func NewCommandQueue(db *sql.DB) service.CommandQueue {
	return &commandQueue{db: db}
}

func (q *commandQueue) Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *service.Command) error {
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

// Dequeue claims the next available command for the cluster using a two-phase
// approach: the row is marked with claimed_at/claimed_by but not deleted.
// The row is only deleted when Acknowledge is called, providing at-least-once
// delivery semantics. RequeueStaleClaims resets abandoned claims.
func (q *commandQueue) Dequeue(ctx context.Context, clusterID uuid.UUID) (*service.Command, error) {
	var cmd service.Command
	var payload []byte
	err := q.db.QueryRowContext(ctx,
		`UPDATE command_queue
		    SET claimed_at = NOW(), claimed_by = $1
		  WHERE id = (
		        SELECT id FROM command_queue
		         WHERE cluster_id = $1
		           AND claimed_at IS NULL
		         ORDER BY created_at ASC
		         LIMIT 1
		         FOR UPDATE SKIP LOCKED
		  )
		  RETURNING id, deployment_id, type, payload, created_at`,
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

// Acknowledge stores the command result and hard-deletes the claimed queue row.
// Only result.Output is stored in the output column; success and error are
// stored in their own columns.
func (q *commandQueue) Acknowledge(ctx context.Context, cmdID uuid.UUID, result *service.CommandResult) error {
	_, err := q.db.ExecContext(ctx,
		"INSERT INTO command_results (command_id, success, output, error, completed_at) VALUES ($1, $2, $3, $4, $5)",
		cmdID, result.Success, []byte(result.Output), result.Error, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert command result: %w", err)
	}
	_, err = q.db.ExecContext(ctx,
		"DELETE FROM command_queue WHERE id = $1",
		cmdID,
	)
	if err != nil {
		return fmt.Errorf("delete command from queue: %w", err)
	}
	return nil
}

// CancelDeployment hard-deletes all unclaimed queue rows for a deployment.
func (q *commandQueue) CancelDeployment(ctx context.Context, deploymentID uuid.UUID) error {
	_, err := q.db.ExecContext(ctx,
		"DELETE FROM command_queue WHERE deployment_id = $1 AND claimed_at IS NULL",
		deploymentID,
	)
	if err != nil {
		return fmt.Errorf("cancel deployment commands: %w", err)
	}
	return nil
}

// RequeueStaleClaims resets claimed_at for rows that have been claimed longer
// than maxAge without being acknowledged.
func (q *commandQueue) RequeueStaleClaims(ctx context.Context, maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge)
	_, err := q.db.ExecContext(ctx,
		`UPDATE command_queue
		    SET claimed_at = NULL, claimed_by = NULL
		  WHERE claimed_at IS NOT NULL
		    AND claimed_at < $1`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("requeue stale claims: %w", err)
	}
	return nil
}

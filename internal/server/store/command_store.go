package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type commandStore struct{ db DBTX }

// NewCommandStore returns a CommandRepository backed by the commands table.
func NewCommandStore(db DBTX) CommandRepository {
	return &commandStore{db: db}
}

func (s *commandStore) Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *domain.Command) error {
	payload, err := json.Marshal(cmd.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO commands (id, cluster_id, deployment_id, type, payload, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'queued', $6, $6)`,
		cmd.ID, clusterID, cmd.DeploymentID, cmd.Type, payload, cmd.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue command: %w", err)
	}
	return nil
}

// Dequeue atomically transitions the next queued command to dispatched using
// SELECT FOR UPDATE SKIP LOCKED. Returns nil if the queue is empty.
func (s *commandStore) Dequeue(ctx context.Context, clusterID uuid.UUID) (*domain.Command, error) {
	var cmd domain.Command
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		`UPDATE commands SET status='dispatched', updated_at=NOW()
		  WHERE id = (
		        SELECT id FROM commands
		         WHERE cluster_id = $1
		           AND status = 'queued'
		         ORDER BY created_at ASC
		         LIMIT 1
		         FOR UPDATE SKIP LOCKED
		  )
		  RETURNING id, cluster_id, deployment_id, type, payload, created_at, updated_at`,
		clusterID,
	).Scan(&cmd.ID, &cmd.ClusterID, &cmd.DeploymentID, &cmd.Type, &payload, &cmd.CreatedAt, &cmd.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeue command: %w", err)
	}
	cmd.Payload = payload
	cmd.Status = domain.CommandStatusDispatched
	return &cmd, nil
}

func (s *commandStore) MarkSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='succeeded', output=$2, updated_at=$3
		  WHERE id = $1`,
		commandID, []byte(output), time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark command succeeded: %w", err)
	}
	return nil
}

func (s *commandStore) MarkFailed(ctx context.Context, commandID uuid.UUID, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='failed', error=$2, updated_at=$3
		  WHERE id = $1`,
		commandID, errMsg, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark command failed: %w", err)
	}
	return nil
}

// Requeue transitions a dispatched command back to queued. The AND status guard
// ensures already-completed commands cannot be re-queued by a late disconnect.
func (s *commandStore) Requeue(ctx context.Context, commandID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='queued', updated_at=NOW()
		  WHERE id = $1 AND status = 'dispatched'`,
		commandID,
	)
	if err != nil {
		return fmt.Errorf("requeue command: %w", err)
	}
	return nil
}

// CancelDeployment transitions all queued commands for a deployment to
// cancelled. Already-dispatched commands are left untouched.
func (s *commandStore) CancelDeployment(ctx context.Context, deploymentID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='cancelled', updated_at=NOW()
		  WHERE deployment_id = $1 AND status = 'queued'`,
		deploymentID,
	)
	if err != nil {
		return fmt.Errorf("cancel deployment commands: %w", err)
	}
	return nil
}

// commandLogStore implements CommandLogRepository.
type commandLogStore struct{ db DBTX }

// NewCommandLogStore returns a CommandLogRepository backed by the command_logs table.
func NewCommandLogStore(db DBTX) CommandLogRepository {
	return &commandLogStore{db: db}
}

func (s *commandLogStore) Append(ctx context.Context, commandID uuid.UUID, line string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO command_logs (command_id, line) VALUES ($1, $2)`,
		commandID, line,
	)
	if err != nil {
		return fmt.Errorf("append command log: %w", err)
	}
	return nil
}

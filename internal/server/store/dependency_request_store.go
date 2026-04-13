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

type DependencyRequestStore struct {
	db DBTX
}

func NewDependencyRequestStore(db DBTX) *DependencyRequestStore {
	return &DependencyRequestStore{db: db}
}

func (s *DependencyRequestStore) Create(ctx context.Context, req *domain.DependencyDeploymentRequest) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO dependency_deployment_requests (id, deployment_id, command_id, dependency_name, status) VALUES ($1, $2, $3, $4, $5)",
		req.ID, req.DeploymentID, req.CommandID, req.DependencyName, req.Status,
	)
	if err != nil {
		return fmt.Errorf("create dependency request: %w", err)
	}
	return nil
}

func (s *DependencyRequestStore) GetByCommandID(ctx context.Context, commandID uuid.UUID) (*domain.DependencyDeploymentRequest, error) {
	var req domain.DependencyDeploymentRequest
	var output []byte
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, deployment_id, command_id, dependency_name, status, output, completed_at FROM dependency_deployment_requests WHERE command_id = $1",
		commandID,
	).Scan(&req.ID, &req.DeploymentID, &req.CommandID, &req.DependencyName, &req.Status, &output, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get dependency request: %w", err)
	}
	if output != nil {
		req.Output = json.RawMessage(output)
	}
	if completedAt.Valid {
		req.CompletedAt = &completedAt.Time
	}
	return &req, nil
}

func (s *DependencyRequestStore) MarkSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE dependency_deployment_requests SET status = $1, output = $2, completed_at = $3 WHERE command_id = $4",
		domain.DependencyRequestStatusSucceeded, []byte(output), time.Now(), commandID,
	)
	if err != nil {
		return fmt.Errorf("mark dependency request succeeded: %w", err)
	}
	return nil
}

func (s *DependencyRequestStore) MarkFailed(ctx context.Context, commandID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE dependency_deployment_requests SET status = $1, completed_at = $2 WHERE command_id = $3",
		domain.DependencyRequestStatusFailed, time.Now(), commandID,
	)
	if err != nil {
		return fmt.Errorf("mark dependency request failed: %w", err)
	}
	return nil
}

// AllComplete returns (allSucceeded, anyFailed, err).
func (s *DependencyRequestStore) AllComplete(ctx context.Context, deploymentID uuid.UUID) (bool, bool, error) {
	var total, succeeded, failed int
	err := s.db.QueryRowContext(ctx,
		`SELECT
		     COUNT(*),
		     COUNT(*) FILTER (WHERE status = $2),
		     COUNT(*) FILTER (WHERE status = $3)
		   FROM dependency_deployment_requests
		  WHERE deployment_id = $1`,
		deploymentID,
		domain.DependencyRequestStatusSucceeded,
		domain.DependencyRequestStatusFailed,
	).Scan(&total, &succeeded, &failed)
	if err != nil {
		return false, false, fmt.Errorf("count dependency request statuses: %w", err)
	}
	return total > 0 && succeeded == total, failed > 0, nil
}

func (s *DependencyRequestStore) GetOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT dependency_name, output FROM dependency_deployment_requests WHERE deployment_id = $1 AND status = $2",
		deploymentID, domain.DependencyRequestStatusSucceeded,
	)
	if err != nil {
		return nil, fmt.Errorf("get dependency outputs: %w", err)
	}
	defer rows.Close()

	outputs := make(map[string]json.RawMessage)
	for rows.Next() {
		var name string
		var output []byte
		if err := rows.Scan(&name, &output); err != nil {
			return nil, fmt.Errorf("scan dependency output: %w", err)
		}
		if output != nil {
			outputs[name] = json.RawMessage(output)
		}
	}
	return outputs, rows.Err()
}

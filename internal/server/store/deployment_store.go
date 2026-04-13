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

type DeploymentStore struct {
	db DBTX
}

func NewDeploymentStore(db DBTX) *DeploymentStore {
	return &DeploymentStore{db: db}
}

func (s *DeploymentStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	return s.scanOne(ctx,
		"SELECT id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, helm_command_id, created_at, completed_at FROM deployments WHERE id = $1",
		id,
	)
}

func (s *DeploymentStore) GetByHelmCommandID(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error) {
	return s.scanOne(ctx,
		"SELECT id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, helm_command_id, created_at, completed_at FROM deployments WHERE helm_command_id = $1",
		cmdID,
	)
}

func (s *DeploymentStore) scanOne(ctx context.Context, query string, arg uuid.UUID) (*domain.Deployment, error) {
	var d domain.Deployment
	var configJSON []byte
	var helmCmdID sql.NullString
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, arg).Scan(
		&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON,
		&d.Status, &d.TriggeredBy, &helmCmdID, &d.CreatedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment: %w", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	if helmCmdID.Valid {
		id, _ := uuid.Parse(helmCmdID.String)
		d.HelmCommandID = &id
	}
	if completedAt.Valid {
		d.CompletedAt = &completedAt.Time
	}
	if err := json.Unmarshal(configJSON, &d.ServiceConfigSnapshot); err != nil {
		return nil, fmt.Errorf("unmarshal config snapshot: %w", err)
	}
	return &d, nil
}

func (s *DeploymentStore) ListByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, helm_command_id, created_at, completed_at FROM deployments WHERE service_id = $1 ORDER BY created_at DESC", serviceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		var d domain.Deployment
		var configJSON []byte
		var helmCmdID sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON, &d.Status, &d.TriggeredBy, &helmCmdID, &d.CreatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		if helmCmdID.Valid {
			id, _ := uuid.Parse(helmCmdID.String)
			d.HelmCommandID = &id
		}
		if completedAt.Valid {
			d.CompletedAt = &completedAt.Time
		}
		if err := json.Unmarshal(configJSON, &d.ServiceConfigSnapshot); err != nil {
			return nil, fmt.Errorf("unmarshal config snapshot: %w", err)
		}
		deployments = append(deployments, d)
	}
	return deployments, rows.Err()
}

func (s *DeploymentStore) Create(ctx context.Context, d *domain.Deployment) error {
	configJSON, err := json.Marshal(d.ServiceConfigSnapshot)
	if err != nil {
		return fmt.Errorf("marshal config snapshot: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		"INSERT INTO deployments (id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, helm_command_id, created_at, completed_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
		d.ID, d.ServiceID, d.EnvironmentID, d.ImageTag, configJSON, d.Status, d.TriggeredBy, d.HelmCommandID, d.CreatedAt, d.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	return nil
}

func (s *DeploymentStore) SetHelmCommandID(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE deployments SET helm_command_id = $1 WHERE id = $2",
		cmdID, id,
	)
	if err != nil {
		return fmt.Errorf("set helm command id: %w", err)
	}
	return nil
}

func (s *DeploymentStore) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE deployments SET status = $1, completed_at = $2 WHERE id = $3",
		status, completedAt, id,
	)
	if err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}
	return nil
}

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
	db *sql.DB
}

func NewDeploymentStore(db *sql.DB) *DeploymentStore {
	return &DeploymentStore{db: db}
}

func (s *DeploymentStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	var d domain.Deployment
	var configJSON []byte
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, created_at, completed_at FROM deployments WHERE id = $1", id,
	).Scan(&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON, &d.Status, &d.TriggeredBy, &d.CreatedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
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
		"SELECT id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, created_at, completed_at FROM deployments WHERE service_id = $1 ORDER BY created_at DESC", serviceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		var d domain.Deployment
		var configJSON []byte
		var completedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON, &d.Status, &d.TriggeredBy, &d.CreatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
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
		"INSERT INTO deployments (id, service_id, environment_id, image_tag, service_config_snapshot, status, triggered_by, created_at, completed_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
		d.ID, d.ServiceID, d.EnvironmentID, d.ImageTag, configJSON, d.Status, d.TriggeredBy, d.CreatedAt, d.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("create deployment: %w", err)
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

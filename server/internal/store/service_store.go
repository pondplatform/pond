package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type ServiceStore struct {
	db DBTX
}

func NewServiceStore(db DBTX) *ServiceStore {
	return &ServiceStore{db: db}
}

const serviceColumns = "id, project_id, name, current_deployment_id, running_deployment_id, created_at"

func scanService(row rowScanner) (*domain.Service, error) {
	var svc domain.Service
	if err := row.Scan(&svc.ID, &svc.ProjectID, &svc.Name, &svc.CurrentDeploymentID, &svc.RunningDeploymentID, &svc.CreatedAt); err != nil {
		return nil, err
	}
	return &svc, nil
}

// Lock takes the service's row lock for the rest of the transaction.
func (s *ServiceStore) Lock(ctx context.Context, id uuid.UUID) error {
	var locked uuid.UUID
	err := s.db.QueryRowContext(ctx, "SELECT id FROM services WHERE id = $1 FOR UPDATE", id).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return api.NotFound("service %s not found", id)
	}
	if err != nil {
		return fmt.Errorf("lock service: %w", err)
	}
	return nil
}

func (s *ServiceStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	svc, err := scanService(s.db.QueryRowContext(ctx,
		"SELECT "+serviceColumns+" FROM services WHERE id = $1", id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api.NotFound("service %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get service: %w", err)
	}
	return svc, nil
}

func (s *ServiceStore) GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error) {
	svc, err := scanService(s.db.QueryRowContext(ctx,
		"SELECT "+serviceColumns+" FROM services WHERE project_id = $1 AND name = $2",
		projectID, name,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api.NotFound("service %q not found in project %s", name, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("get service: %w", err)
	}
	return svc, nil
}

func (s *ServiceStore) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+serviceColumns+" FROM services WHERE project_id = $1", projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	var services []domain.Service
	for rows.Next() {
		svc, err := scanService(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		services = append(services, *svc)
	}
	return services, rows.Err()
}

func (s *ServiceStore) Create(ctx context.Context, svc *domain.Service) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO services (id, project_id, name, created_at) VALUES ($1, $2, $3, $4)",
		svc.ID, svc.ProjectID, svc.Name, svc.CreatedAt,
	)
	if pgCode(err) == pgUniqueViolation {
		return api.AlreadyExists("service %q already exists in project %s", svc.Name, svc.ProjectID)
	}
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	return nil
}

func (s *ServiceStore) SetCurrentDeployment(ctx context.Context, serviceID, deploymentID uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE services SET current_deployment_id = $2 WHERE id = $1", serviceID, deploymentID,
	); err != nil {
		return fmt.Errorf("set current deployment of service %s: %w", serviceID, err)
	}
	return nil
}

func (s *ServiceStore) SetRunningDeployment(ctx context.Context, serviceID, deploymentID uuid.UUID) error {
	if _, err := s.db.ExecContext(ctx,
		"UPDATE services SET running_deployment_id = $2 WHERE id = $1", serviceID, deploymentID,
	); err != nil {
		return fmt.Errorf("set running deployment of service %s: %w", serviceID, err)
	}
	return nil
}

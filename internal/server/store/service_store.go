package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type ServiceStore struct {
	db *sql.DB
}

func NewServiceStore(db *sql.DB) *ServiceStore {
	return &ServiceStore{db: db}
}

func (s *ServiceStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	var svc domain.Service
	err := s.db.QueryRowContext(ctx,
		"SELECT id, project_id, name, created_at FROM services WHERE id = $1", id,
	).Scan(&svc.ID, &svc.ProjectID, &svc.Name, &svc.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("service %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get service: %w", err)
	}
	return &svc, nil
}

func (s *ServiceStore) GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Service, error) {
	var svc domain.Service
	err := s.db.QueryRowContext(ctx,
		"SELECT id, project_id, name, created_at FROM services WHERE project_id = $1 AND name = $2",
		projectID, name,
	).Scan(&svc.ID, &svc.ProjectID, &svc.Name, &svc.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("service %q: %w", name, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get service: %w", err)
	}
	return &svc, nil
}

func (s *ServiceStore) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, project_id, name, created_at FROM services WHERE project_id = $1", projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	var services []domain.Service
	for rows.Next() {
		var svc domain.Service
		if err := rows.Scan(&svc.ID, &svc.ProjectID, &svc.Name, &svc.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		services = append(services, svc)
	}
	return services, rows.Err()
}

func (s *ServiceStore) Create(ctx context.Context, svc *domain.Service) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO services (id, project_id, name, created_at) VALUES ($1, $2, $3, $4)",
		svc.ID, svc.ProjectID, svc.Name, svc.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	return nil
}

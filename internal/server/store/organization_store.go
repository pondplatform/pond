package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type OrganizationStore struct {
	db DBTX
}

func NewOrganizationStore(db DBTX) *OrganizationStore {
	return &OrganizationStore{db: db}
}

func (s *OrganizationStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Organization, error) {
	var org domain.Organization
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at FROM organizations WHERE id = $1", id,
	).Scan(&org.ID, &org.Name, &org.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("organization %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	return &org, nil
}

func (s *OrganizationStore) GetByName(ctx context.Context, name string) (*domain.Organization, error) {
	var org domain.Organization
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at FROM organizations WHERE name = $1", name,
	).Scan(&org.ID, &org.Name, &org.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("organization %q: %w", name, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	return &org, nil
}

func (s *OrganizationStore) Create(ctx context.Context, org *domain.Organization) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO organizations (id, name, created_at) VALUES ($1, $2, $3)",
		org.ID, org.Name, org.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create organization: %w", err)
	}
	return nil
}

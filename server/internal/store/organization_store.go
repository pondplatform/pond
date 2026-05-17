package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
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
		return nil, fmt.Errorf("organization %s: %w", id, api.ErrNotFound)
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
		return nil, fmt.Errorf("organization %q: %w", name, api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	return &org, nil
}

func (s *OrganizationStore) List(ctx context.Context, limit int, cursor string) ([]domain.Organization, error) {
	query := "SELECT id, name, created_at FROM organizations ORDER BY created_at DESC, id DESC LIMIT $1"
	args := []any{limit + 1} // fetch one extra to determine if there's a next page

	if cursor != "" {
		query = "SELECT id, name, created_at FROM organizations WHERE created_at <= $2 ORDER BY created_at DESC, id DESC LIMIT $1"
		args = append(args, cursor)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var orgs []domain.Organization
	for rows.Next() {
		var org domain.Organization
		if err := rows.Scan(&org.ID, &org.Name, &org.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		orgs = append(orgs, org)
	}
	return orgs, rows.Err()
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

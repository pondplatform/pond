package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type ProjectStore struct {
	db DBTX
}

func NewProjectStore(db DBTX) *ProjectStore {
	return &ProjectStore{db: db}
}

func (s *ProjectStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Project, error) {
	var p domain.Project
	var rootEnvID sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT id, organization_id, name, root_environment_id, created_at FROM projects WHERE id = $1", id,
	).Scan(&p.ID, &p.OrganizationID, &p.Name, &rootEnvID, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %s: %w", id, api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	if rootEnvID.Valid {
		id, _ := uuid.Parse(rootEnvID.String)
		p.RootEnvironmentID = &id
	}
	return &p, nil
}

func (s *ProjectStore) GetByName(ctx context.Context, orgID uuid.UUID, name string) (*domain.Project, error) {
	var p domain.Project
	var rootEnvID sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT id, organization_id, name, root_environment_id, created_at FROM projects WHERE organization_id = $1 AND name = $2",
		orgID, name,
	).Scan(&p.ID, &p.OrganizationID, &p.Name, &rootEnvID, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("project %q: %w", name, api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	if rootEnvID.Valid {
		id, _ := uuid.Parse(rootEnvID.String)
		p.RootEnvironmentID = &id
	}
	return &p, nil
}

func (s *ProjectStore) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, organization_id, name, root_environment_id, created_at FROM projects WHERE organization_id = $1", orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		var p domain.Project
		var rootEnvID sql.NullString
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Name, &rootEnvID, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		if rootEnvID.Valid {
			id, _ := uuid.Parse(rootEnvID.String)
			p.RootEnvironmentID = &id
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *ProjectStore) Create(ctx context.Context, project *domain.Project) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO projects (id, organization_id, name, root_environment_id, created_at) VALUES ($1, $2, $3, $4, $5)",
		project.ID, project.OrganizationID, project.Name, project.RootEnvironmentID, project.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

func (s *ProjectStore) SetRootEnvironment(ctx context.Context, projectID, envID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE projects SET root_environment_id = $1 WHERE id = $2",
		envID, projectID,
	)
	if err != nil {
		return fmt.Errorf("set root environment: %w", err)
	}
	return nil
}

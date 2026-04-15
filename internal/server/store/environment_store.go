package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type EnvironmentStore struct {
	db DBTX
}

func NewEnvironmentStore(db DBTX) *EnvironmentStore {
	return &EnvironmentStore{db: db}
}

func (s *EnvironmentStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Environment, error) {
	var env domain.Environment
	var parentID sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT id, project_id, parent_environment_id, name, namespace, default_ingress_base_host, cluster_id, created_at FROM environments WHERE id = $1", id,
	).Scan(&env.ID, &env.ProjectID, &parentID, &env.Name, &env.Namespace, &env.DefaultIngressBaseHost, &env.ClusterID, &env.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("environment %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if parentID.Valid {
		id, _ := uuid.Parse(parentID.String)
		env.ParentEnvironmentID = &id
	}
	return &env, nil
}

func (s *EnvironmentStore) GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error) {
	var env domain.Environment
	var parentID sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT id, project_id, parent_environment_id, name, namespace, default_ingress_base_host, cluster_id, created_at FROM environments WHERE project_id = $1 AND name = $2",
		projectID, name,
	).Scan(&env.ID, &env.ProjectID, &parentID, &env.Name, &env.Namespace, &env.DefaultIngressBaseHost, &env.ClusterID, &env.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("environment %q: %w", name, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	if parentID.Valid {
		id, _ := uuid.Parse(parentID.String)
		env.ParentEnvironmentID = &id
	}
	return &env, nil
}

func (s *EnvironmentStore) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, project_id, parent_environment_id, name, namespace, default_ingress_base_host, cluster_id, created_at FROM environments WHERE project_id = $1", projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()

	var envs []domain.Environment
	for rows.Next() {
		var env domain.Environment
		var parentID sql.NullString
		if err := rows.Scan(&env.ID, &env.ProjectID, &parentID, &env.Name, &env.Namespace, &env.DefaultIngressBaseHost, &env.ClusterID, &env.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		if parentID.Valid {
			id, _ := uuid.Parse(parentID.String)
			env.ParentEnvironmentID = &id
		}
		envs = append(envs, env)
	}
	return envs, rows.Err()
}

func (s *EnvironmentStore) Create(ctx context.Context, env *domain.Environment) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO environments (id, project_id, parent_environment_id, name, namespace, default_ingress_base_host, cluster_id, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		env.ID, env.ProjectID, env.ParentEnvironmentID, env.Name, env.Namespace, env.DefaultIngressBaseHost, env.ClusterID, env.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create environment: %w", err)
	}
	return nil
}

func (s *EnvironmentStore) Update(ctx context.Context, env *domain.Environment) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE environments SET parent_environment_id = $1, namespace = $2, default_ingress_base_host = $3, cluster_id = $4 WHERE id = $5",
		env.ParentEnvironmentID, env.Namespace, env.DefaultIngressBaseHost, env.ClusterID, env.ID,
	)
	if err != nil {
		return fmt.Errorf("update environment: %w", err)
	}
	return nil
}

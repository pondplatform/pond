package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type EnvironmentStore struct {
	db DBTX
}

func NewEnvironmentStore(db DBTX) *EnvironmentStore {
	return &EnvironmentStore{db: db}
}

const environmentColumns = "id, project_id, parent_environment_id, name, namespace, default_ingress_base_host, gateway_port, gateway_tls, cluster_id, created_at"

func scanEnvironment(row rowScanner) (*domain.Environment, error) {
	var env domain.Environment
	var parentID uuid.NullUUID
	var gatewayPort sql.NullInt32
	if err := row.Scan(&env.ID, &env.ProjectID, &parentID, &env.Name, &env.Namespace, &env.DefaultIngressBaseHost, &gatewayPort, &env.GatewayTLS, &env.ClusterID, &env.CreatedAt); err != nil {
		return nil, err
	}
	if parentID.Valid {
		env.ParentEnvironmentID = &parentID.UUID
	}
	if gatewayPort.Valid {
		env.GatewayPort = &gatewayPort.Int32
	}
	return &env, nil
}

func (s *EnvironmentStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Environment, error) {
	env, err := scanEnvironment(s.db.QueryRowContext(ctx,
		"SELECT "+environmentColumns+" FROM environments WHERE id = $1", id))
	if err == sql.ErrNoRows {
		return nil, api.NotFound("environment %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	return env, nil
}

func (s *EnvironmentStore) GetByName(ctx context.Context, projectID uuid.UUID, name string) (*domain.Environment, error) {
	env, err := scanEnvironment(s.db.QueryRowContext(ctx,
		"SELECT "+environmentColumns+" FROM environments WHERE project_id = $1 AND name = $2", projectID, name))
	if err == sql.ErrNoRows {
		return nil, api.NotFound("environment %q not found in project %s", name, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	return env, nil
}

func (s *EnvironmentStore) ListByProject(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+environmentColumns+" FROM environments WHERE project_id = $1", projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()

	var envs []domain.Environment
	for rows.Next() {
		env, err := scanEnvironment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		envs = append(envs, *env)
	}
	return envs, rows.Err()
}

func (s *EnvironmentStore) Create(ctx context.Context, env *domain.Environment) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO environments ("+environmentColumns+") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
		env.ID, env.ProjectID, env.ParentEnvironmentID, env.Name, env.Namespace, env.DefaultIngressBaseHost, env.GatewayPort, env.GatewayTLS, env.ClusterID, env.CreatedAt,
	)
	if pgCode(err) == pgUniqueViolation {
		return api.AlreadyExists("environment %q already exists in project %s", env.Name, env.ProjectID)
	}
	if err != nil {
		return fmt.Errorf("create environment: %w", err)
	}
	return nil
}

func (s *EnvironmentStore) Update(ctx context.Context, env *domain.Environment) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE environments SET parent_environment_id = $1, namespace = $2, default_ingress_base_host = $3, gateway_port = $4, gateway_tls = $5, cluster_id = $6 WHERE id = $7",
		env.ParentEnvironmentID, env.Namespace, env.DefaultIngressBaseHost, env.GatewayPort, env.GatewayTLS, env.ClusterID, env.ID,
	)
	if pgCode(err) == pgForeignKeyViolation {
		return api.InvalidInput("environment %s references a parent environment or cluster that does not exist", env.ID)
	}
	if err != nil {
		return fmt.Errorf("update environment: %w", err)
	}
	return nil
}

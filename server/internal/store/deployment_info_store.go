package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type deploymentInfoStore struct{ db DBTX }

// NewDeploymentInfoStore returns a DeploymentInfoStore backed by the deployments,
// commands, command_logs, and dependency_deployments tables.
func NewDeploymentInfoStore(db DBTX) DeploymentInfoStore {
	return &deploymentInfoStore{db: db}
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// ── Deployment operations ────────────────────────────────────────────────────

// startedSQL is true once an agent has picked up any command of deployment d.
const startedSQL = `EXISTS (SELECT 1 FROM commands c WHERE c.deployment_id = d.id AND c.status <> '` +
	string(domain.CommandStatusQueued) + `')`

const deploymentColumns = `d.id, d.service_id, d.environment_id, d.image_tag, d.service_config_snapshot,
	d.status, d.triggered_by, d.helm_command_id, d.created_at, d.completed_at, d.error, ` + startedSQL

func scanDeployment(row rowScanner) (*domain.Deployment, error) {
	var d domain.Deployment
	var configJSON []byte
	var triggeredBy sql.NullString
	if err := row.Scan(
		&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON,
		&d.Status, &triggeredBy, &d.HelmCommandID, &d.CreatedAt, &d.CompletedAt, &d.Error, &d.Started,
	); err != nil {
		return nil, err
	}
	d.TriggeredBy = triggeredBy.String
	if err := json.Unmarshal(configJSON, &d.ServiceConfigSnapshot); err != nil {
		return nil, fmt.Errorf("unmarshal config snapshot: %w", err)
	}
	return &d, nil
}

func (s *deploymentInfoStore) getDeployment(ctx context.Context, query string, id uuid.UUID) (*domain.Deployment, error) {
	d, err := scanDeployment(s.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api.NotFound("deployment %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return d, nil
}

func (s *deploymentInfoStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	return s.getDeployment(ctx, `SELECT `+deploymentColumns+` FROM deployments d WHERE d.id = $1`, id)
}

func (s *deploymentInfoStore) LockDeployment(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	return s.getDeployment(ctx, `SELECT `+deploymentColumns+` FROM deployments d WHERE d.id = $1 FOR UPDATE OF d`, id)
}

func (s *deploymentInfoStore) ListByServiceFiltered(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error) {
	query := `SELECT ` + deploymentColumns + ` FROM deployments d WHERE d.service_id = $1`
	args := []any{serviceID}
	argIdx := 2

	if environmentID != nil {
		query += fmt.Sprintf(" AND d.environment_id = $%d", argIdx)
		args = append(args, *environmentID)
		argIdx++
	}

	if status != nil {
		// pending and running are both in_progress, told apart by startedSQL.
		stored := domain.DeploymentState(*status)
		switch *status {
		case api.DeploymentStatusPending:
			stored = domain.DeploymentInProgress
			query += " AND NOT " + startedSQL
		case api.DeploymentStatusRunning:
			stored = domain.DeploymentInProgress
			query += " AND " + startedSQL
		}
		query += fmt.Sprintf(" AND d.status = $%d", argIdx)
		args = append(args, stored)
		argIdx++
	}

	if cursor != "" {
		query += fmt.Sprintf(" AND d.created_at <= $%d", argIdx)
		args = append(args, cursor)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY d.created_at DESC, d.id DESC LIMIT $%d", argIdx)
	args = append(args, limit+1) // fetch one extra for pagination

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		deployments = append(deployments, *d)
	}
	return deployments, rows.Err()
}

func (s *deploymentInfoStore) Create(ctx context.Context, d *domain.Deployment) error {
	configJSON, err := json.Marshal(d.ServiceConfigSnapshot)
	if err != nil {
		return fmt.Errorf("marshal config snapshot: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO deployments
		        (id, service_id, environment_id, image_tag, service_config_snapshot,
		         status, triggered_by, helm_command_id, created_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		d.ID, d.ServiceID, d.EnvironmentID, d.ImageTag, configJSON,
		d.Status, d.TriggeredBy, d.HelmCommandID, d.CreatedAt, d.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) SaveDeployment(ctx context.Context, d *domain.Deployment) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = $2, error = $3, completed_at = $4 WHERE id = $1`,
		d.ID, d.Status, d.Error, d.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("save deployment: %w", err)
	}
	return nil
}

// ── Command operations ───────────────────────────────────────────────────────

const commandColumns = `id, cluster_id, deployment_id, type, phase, payload, status, output, error, created_at, updated_at`

func scanCommand(row rowScanner) (*domain.Command, error) {
	var cmd domain.Command
	var payload, output []byte
	var errStr sql.NullString
	if err := row.Scan(&cmd.ID, &cmd.ClusterID, &cmd.DeploymentID, &cmd.Type, &cmd.Phase, &payload, &cmd.Status,
		&output, &errStr, &cmd.CreatedAt, &cmd.UpdatedAt); err != nil {
		return nil, err
	}
	if payload != nil {
		cmd.Payload = payload
	}
	if output != nil {
		cmd.Output = output
	}
	cmd.Error = errStr.String
	return &cmd, nil
}

func (s *deploymentInfoStore) listCommands(ctx context.Context, query string, args ...any) ([]*domain.Command, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	defer rows.Close()

	var cmds []*domain.Command
	for rows.Next() {
		cmd, err := scanCommand(rows)
		if err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		cmds = append(cmds, cmd)
	}
	return cmds, rows.Err()
}

func (s *deploymentInfoStore) CreateCommand(ctx context.Context, cmd *domain.Command) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO commands (id, cluster_id, deployment_id, type, phase, payload, status, output, error, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		cmd.ID, cmd.ClusterID, cmd.DeploymentID, cmd.Type, cmd.Phase, nullableJSON(cmd.Payload), cmd.Status,
		nullableJSON(cmd.Output), cmd.Error, cmd.CreatedAt, cmd.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create command: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) GetCommand(ctx context.Context, id uuid.UUID) (*domain.Command, error) {
	cmd, err := scanCommand(s.db.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM commands WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, api.NotFound("command %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get command: %w", err)
	}
	return cmd, nil
}

func (s *deploymentInfoStore) UpdateCommand(ctx context.Context, cmd *domain.Command) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET payload = $2, status = $3, output = $4, error = $5, updated_at = $6 WHERE id = $1`,
		cmd.ID, nullableJSON(cmd.Payload), cmd.Status, nullableJSON(cmd.Output), cmd.Error, cmd.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update command: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) ListCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID) ([]*domain.Command, error) {
	return s.listCommands(ctx,
		`SELECT `+commandColumns+` FROM commands WHERE deployment_id = $1 ORDER BY phase, created_at, id`,
		deploymentID,
	)
}

func (s *deploymentInfoStore) ClaimNextCommand(ctx context.Context, clusterID uuid.UUID, at time.Time) (*domain.Command, error) {
	// SKIP LOCKED lets several agents of a cluster claim side by side, each
	// getting a different command.
	cmd, err := scanCommand(s.db.QueryRowContext(ctx,
		`UPDATE commands SET status = $2, updated_at = $3
		  WHERE id = (
		        SELECT c.id FROM commands c JOIN deployments d ON d.id = c.deployment_id
		         WHERE c.cluster_id = $1 AND c.status = $4 AND d.status = $5
		           AND NOT EXISTS (SELECT 1 FROM commands p
		                            WHERE p.deployment_id = c.deployment_id AND p.phase < c.phase AND p.status <> $6)
		         ORDER BY c.created_at, c.id
		         LIMIT 1
		           FOR UPDATE OF c SKIP LOCKED)
		 RETURNING `+commandColumns,
		clusterID, domain.CommandStatusDispatched, at,
		domain.CommandStatusQueued, domain.DeploymentInProgress, domain.CommandStatusSucceeded,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim next command: %w", err)
	}
	return cmd, nil
}

func (s *deploymentInfoStore) UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, to domain.CommandStatus, from ...domain.CommandStatus) error {
	fromStatuses := make([]string, len(from))
	for i, status := range from {
		fromStatuses[i] = string(status)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = $2, updated_at = NOW()
		  WHERE deployment_id = $1 AND status = ANY($3)`,
		deploymentID, to, pq.Array(fromStatuses),
	)
	if err != nil {
		return fmt.Errorf("update commands by deployment: %w", err)
	}
	return nil
}

// ── Command log operations ───────────────────────────────────────────────────

func (s *deploymentInfoStore) AppendLog(ctx context.Context, clusterID, commandID uuid.UUID, line string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO command_logs (command_id, line)
		 SELECT id, $3 FROM commands WHERE id = $1 AND cluster_id = $2`,
		commandID, clusterID, line,
	)
	if err != nil {
		return fmt.Errorf("append command log: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT line, logged_at FROM command_logs WHERE command_id = $1 ORDER BY logged_at`,
		commandID,
	)
	if err != nil {
		return nil, fmt.Errorf("query command logs: %w", err)
	}
	defer rows.Close()

	var logs []domain.CommandLog
	for rows.Next() {
		var l domain.CommandLog
		if err := rows.Scan(&l.Line, &l.LoggedAt); err != nil {
			return nil, fmt.Errorf("scan command log: %w", err)
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate command logs: %w", err)
	}
	return logs, nil
}

// ── Dependency config operations ─────────────────────────────────────────────

const depConfigColumns = `id, deployment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, command_id`

func scanDepConfig(row rowScanner) (*domain.DependencyDeployment, error) {
	var cfg domain.DependencyDeployment
	var providerInputs, userConfig []byte
	if err := row.Scan(
		&cfg.ID, &cfg.DeploymentId, &cfg.DependencyName, &cfg.DependencyType, &cfg.Managed,
		&providerInputs, &userConfig, &cfg.CommandID,
	); err != nil {
		return nil, err
	}
	if providerInputs != nil {
		if err := json.Unmarshal(providerInputs, &cfg.ProviderInputs); err != nil {
			return nil, fmt.Errorf("unmarshal provider inputs: %w", err)
		}
	}
	if userConfig != nil {
		if err := json.Unmarshal(userConfig, &cfg.UserConfig); err != nil {
			return nil, fmt.Errorf("unmarshal user config: %w", err)
		}
	}
	return &cfg, nil
}

func (s *deploymentInfoStore) CreateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error {
	providerInputs, err := json.Marshal(cfg.ProviderInputs)
	if err != nil {
		return fmt.Errorf("marshal provider inputs: %w", err)
	}
	userConfig, err := json.Marshal(cfg.UserConfig)
	if err != nil {
		return fmt.Errorf("marshal user config: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dependency_deployments
		        (id, deployment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, command_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		cfg.ID, cfg.DeploymentId, cfg.DependencyName, cfg.DependencyType, cfg.Managed, providerInputs, userConfig, cfg.CommandID,
	)
	if err != nil {
		return fmt.Errorf("create dep config: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) ListDepConfigs(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+depConfigColumns+` FROM dependency_deployments WHERE deployment_id = $1 ORDER BY dependency_name`,
		deploymentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list dep configs: %w", err)
	}
	defer rows.Close()

	var configs []domain.DependencyDeployment
	for rows.Next() {
		cfg, err := scanDepConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("scan dep config: %w", err)
		}
		configs = append(configs, *cfg)
	}
	return configs, rows.Err()
}

// nullableJSON stores an empty message as SQL NULL rather than invalid JSONB.
func nullableJSON(msg json.RawMessage) any {
	if len(msg) == 0 {
		return nil
	}
	return []byte(msg)
}

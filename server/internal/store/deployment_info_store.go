package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type deploymentInfoStore struct{ db DBTX }

// NewDeploymentInfoStore returns a DeploymentInfoStore backed by the deployments,
// commands, command_logs, and dependency_deployments tables.
func NewDeploymentInfoStore(db DBTX) DeploymentInfoStore {
	return &deploymentInfoStore{db: db}
}

// ── Deployment operations ────────────────────────────────────────────────────

func (s *deploymentInfoStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	return s.scanOneDeployment(ctx,
		`SELECT id, service_id, environment_id, image_tag, service_config_snapshot,
		        status, triggered_by, helm_command_id, created_at, completed_at
		   FROM deployments WHERE id = $1`,
		id,
	)
}

func (s *deploymentInfoStore) GetByHelmCommandID(ctx context.Context, cmdID uuid.UUID) (*domain.Deployment, error) {
	return s.scanOneDeployment(ctx,
		`SELECT id, service_id, environment_id, image_tag, service_config_snapshot,
		        status, triggered_by, helm_command_id, created_at, completed_at
		   FROM deployments WHERE helm_command_id = $1`,
		cmdID,
	)
}

func (s *deploymentInfoStore) scanOneDeployment(ctx context.Context, query string, arg uuid.UUID) (*domain.Deployment, error) {
	var d domain.Deployment
	var configJSON []byte
	var helmCmdID sql.NullString
	var triggeredBy sql.NullString
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, arg).Scan(
		&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON,
		&d.Status, &triggeredBy, &helmCmdID, &d.CreatedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment: %w", api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	if triggeredBy.Valid {
		d.TriggeredBy = triggeredBy.String
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

func (s *deploymentInfoStore) ListByService(ctx context.Context, serviceID uuid.UUID) ([]domain.Deployment, error) {
	return s.ListByServiceFiltered(ctx, serviceID, nil, nil, 100, "")
}

func (s *deploymentInfoStore) ListByServiceFiltered(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *domain.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error) {
	query := `SELECT id, service_id, environment_id, image_tag, service_config_snapshot,
	                 status, triggered_by, helm_command_id, created_at, completed_at
	            FROM deployments WHERE service_id = $1`
	args := []any{serviceID}
	argIdx := 2

	if environmentID != nil {
		query += fmt.Sprintf(" AND environment_id = $%d", argIdx)
		args = append(args, *environmentID)
		argIdx++
	}

	if status != nil {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, *status)
		argIdx++
	}

	if cursor != "" {
		query += fmt.Sprintf(" AND created_at <= $%d", argIdx)
		args = append(args, cursor)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", argIdx)
	args = append(args, limit+1) // fetch one extra for pagination

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var deployments []domain.Deployment
	for rows.Next() {
		var d domain.Deployment
		var configJSON []byte
		var helmCmdID sql.NullString
		var triggeredBy sql.NullString
		var completedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.ServiceID, &d.EnvironmentID, &d.ImageTag, &configJSON,
			&d.Status, &triggeredBy, &helmCmdID, &d.CreatedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan deployment: %w", err)
		}
		if triggeredBy.Valid {
			d.TriggeredBy = triggeredBy.String
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

func (s *deploymentInfoStore) SetHelmCommandID(ctx context.Context, id uuid.UUID, cmdID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET helm_command_id = $1 WHERE id = $2`,
		cmdID, id,
	)
	if err != nil {
		return fmt.Errorf("set helm command id: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus, completedAt *time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = $1, completed_at = $2 WHERE id = $3`,
		status, completedAt, id,
	)
	if err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}
	return nil
}

// ── Command operations (pure CRUD) ───────────────────────────────────────────

func (s *deploymentInfoStore) CreateCommand(ctx context.Context, cmd *domain.Command) error {
	payload, err := json.Marshal(cmd.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO commands (id, cluster_id, deployment_id, type, payload, status, output, error, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		cmd.ID, cmd.ClusterID, cmd.DeploymentID, cmd.Type, payload, cmd.Status,
		[]byte(cmd.Output), cmd.Error, cmd.CreatedAt, cmd.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create command: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) GetCommand(ctx context.Context, id uuid.UUID) (*domain.Command, error) {
	var cmd domain.Command
	var payload, output []byte
	var errStr sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, cluster_id, deployment_id, type, payload, status, output, error, created_at, updated_at
		   FROM commands WHERE id = $1`,
		id,
	).Scan(&cmd.ID, &cmd.ClusterID, &cmd.DeploymentID, &cmd.Type, &payload, &cmd.Status,
		&output, &errStr, &cmd.CreatedAt, &cmd.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("command: %w", api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get command: %w", err)
	}
	cmd.Payload = payload
	if output != nil {
		cmd.Output = output
	}
	if errStr.Valid {
		cmd.Error = errStr.String
	}
	return &cmd, nil
}

func (s *deploymentInfoStore) UpdateCommand(ctx context.Context, cmd *domain.Command) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = $2, output = $3, error = $4, updated_at = $5 WHERE id = $1`,
		cmd.ID, cmd.Status, []byte(cmd.Output), cmd.Error, cmd.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update command: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) ListQueuedCommandsByCluster(ctx context.Context, clusterID uuid.UUID) ([]*domain.Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, cluster_id, deployment_id, type, payload, status, output, error, created_at, updated_at
		   FROM commands
		  WHERE cluster_id = $1 AND status = $2
		  ORDER BY created_at ASC`,
		clusterID, domain.CommandStatusQueued,
	)
	if err != nil {
		return nil, fmt.Errorf("list queued commands: %w", err)
	}
	defer rows.Close()

	var cmds []*domain.Command
	for rows.Next() {
		var cmd domain.Command
		var payload, output []byte
		var errStr sql.NullString
		if err := rows.Scan(&cmd.ID, &cmd.ClusterID, &cmd.DeploymentID, &cmd.Type, &payload, &cmd.Status,
			&output, &errStr, &cmd.CreatedAt, &cmd.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan command: %w", err)
		}
		cmd.Payload = payload
		if output != nil {
			cmd.Output = output
		}
		if errStr.Valid {
			cmd.Error = errStr.String
		}
		cmds = append(cmds, &cmd)
	}
	return cmds, rows.Err()
}

func (s *deploymentInfoStore) UpdateCommandsByDeployment(ctx context.Context, deploymentID uuid.UUID, fromStatus, toStatus domain.CommandStatus) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status = $3, updated_at = NOW()
		  WHERE deployment_id = $1 AND status = $2`,
		deploymentID, fromStatus, toStatus,
	)
	if err != nil {
		return fmt.Errorf("update commands by deployment: %w", err)
	}
	return nil
}

// ── Command log operations ───────────────────────────────────────────────────

func (s *deploymentInfoStore) AppendLog(ctx context.Context, commandID uuid.UUID, line string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO command_logs (command_id, line) VALUES ($1, $2)`,
		commandID, line,
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

func (s *deploymentInfoStore) CreateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error {
	providerInputs, err := json.Marshal(cfg.ProviderInputs)
	if err != nil {
		return fmt.Errorf("marshal provider inputs: %w", err)
	}
	userConfig, err := json.Marshal(cfg.UserConfig)
	if err != nil {
		return fmt.Errorf("marshal user config: %w", err)
	}
	var output []byte
	if cfg.Output != nil {
		output = []byte(cfg.Output)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dependency_deployments
		        (id, deployment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, status, command_id, output)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		cfg.ID, cfg.DeploymentId, cfg.DependencyName, cfg.DependencyType, cfg.Managed, providerInputs, userConfig, cfg.Status, cfg.CommandID, output,
	)
	if err != nil {
		return fmt.Errorf("create dep config: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) GetDepConfig(ctx context.Context, deploymentID uuid.UUID, depName string) (*domain.DependencyDeployment, error) {
	var cfg domain.DependencyDeployment
	var providerInputs []byte
	var userConfig []byte
	var output []byte
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, deployment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, status, command_id, output, updated_at
		   FROM dependency_deployments WHERE deployment_id = $1 AND dependency_name = $2`,
		deploymentID, depName,
	).Scan(&cfg.ID, &cfg.DeploymentId, &cfg.DependencyName, &cfg.DependencyType, &cfg.Managed,
		&providerInputs, &userConfig, &cfg.Status, &cfg.CommandID, &output, &completedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("dep config: %w", api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get dep config: %w", err)
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
	if output != nil {
		cfg.Output = json.RawMessage(output)
	}
	if completedAt.Valid {
		cfg.CompletedAt = &completedAt.Time
	}
	return &cfg, nil
}

func (s *deploymentInfoStore) GetDepConfigByCommandID(ctx context.Context, commandID uuid.UUID) (uuid.UUID, *domain.DependencyDeployment, error) {
	var cfg domain.DependencyDeployment
	var deploymentID uuid.UUID
	var output []byte
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, deployment_id, dependency_name, dependency_type, managed, status, command_id, output, updated_at
		   FROM dependency_deployments WHERE command_id = $1`,
		commandID,
	).Scan(&cfg.ID, &deploymentID, &cfg.DependencyName, &cfg.DependencyType, &cfg.Managed,
		&cfg.Status, &cfg.CommandID, &output, &completedAt)
	if err == sql.ErrNoRows {
		return uuid.Nil, nil, nil
	}
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("get dep config by command id: %w", err)
	}
	if output != nil {
		cfg.Output = json.RawMessage(output)
	}
	if completedAt.Valid {
		cfg.CompletedAt = &completedAt.Time
	}
	return deploymentID, &cfg, nil
}

func (s *deploymentInfoStore) MarkDepConfigSucceeded(ctx context.Context, deploymentID uuid.UUID, depName string, output json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE dependency_deployments SET status = $1, output = $2, updated_at = NOW()
		  WHERE deployment_id = $3 AND dependency_name = $4`,
		domain.DependencyDeploymentStatusSucceeded, []byte(output), deploymentID, depName,
	)
	if err != nil {
		return fmt.Errorf("mark dep config succeeded: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) MarkDepConfigFailed(ctx context.Context, deploymentID uuid.UUID, depName string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE dependency_deployments SET status = $1, updated_at = NOW()
		  WHERE deployment_id = $2 AND dependency_name = $3`,
		domain.DependencyDeploymentStatusFailed, deploymentID, depName,
	)
	if err != nil {
		return fmt.Errorf("mark dep config failed: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) AllDepConfigsComplete(ctx context.Context, deploymentID uuid.UUID) (bool, bool, error) {
	var total, succeeded, failed int
	err := s.db.QueryRowContext(ctx,
		`SELECT
		     COUNT(*),
		     COUNT(*) FILTER (WHERE status = $2),
		     COUNT(*) FILTER (WHERE status = $3)
		   FROM dependency_deployments
		  WHERE deployment_id = $1 AND managed = true`,
		deploymentID,
		domain.DependencyDeploymentStatusSucceeded,
		domain.DependencyDeploymentStatusFailed,
	).Scan(&total, &succeeded, &failed)
	if err != nil {
		return false, false, fmt.Errorf("count dep config statuses: %w", err)
	}
	return total > 0 && succeeded == total, failed > 0, nil
}

func (s *deploymentInfoStore) AnyDepConfigAwaitingInput(ctx context.Context, deploymentID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dependency_deployments
		  WHERE deployment_id = $1 AND status = $2`,
		deploymentID, domain.DependencyDeploymentStatusAwaitingInput,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count awaiting input: %w", err)
	}
	return count > 0, nil
}

func (s *deploymentInfoStore) ListDepConfigs(ctx context.Context, deploymentID uuid.UUID) ([]domain.DependencyDeployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, deployment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, output, status, command_id
		   FROM dependency_deployments
		  WHERE deployment_id = $1`,
		deploymentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list dep configs: %w", err)
	}
	defer rows.Close()

	var configs []domain.DependencyDeployment
	for rows.Next() {
		var cfg domain.DependencyDeployment
		var providerInputs, userConfig, output []byte
		if err := rows.Scan(
			&cfg.ID, &cfg.DeploymentId, &cfg.DependencyName, &cfg.DependencyType,
			&cfg.Managed, &providerInputs, &userConfig, &output, &cfg.Status, &cfg.CommandID,
		); err != nil {
			return nil, fmt.Errorf("scan dep config: %w", err)
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
		if output != nil {
			cfg.Output = output
		}
		configs = append(configs, cfg)
	}
	return configs, rows.Err()
}

func (s *deploymentInfoStore) GetDepOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dependency_name, output FROM dependency_deployments
		  WHERE deployment_id = $1 AND status = $2`,
		deploymentID, domain.DependencyDeploymentStatusSucceeded,
	)
	if err != nil {
		return nil, fmt.Errorf("get dep outputs: %w", err)
	}
	defer rows.Close()

	outputs := make(map[string]json.RawMessage)
	for rows.Next() {
		var name string
		var output []byte
		if err := rows.Scan(&name, &output); err != nil {
			return nil, fmt.Errorf("scan dep output: %w", err)
		}
		if output != nil {
			outputs[name] = json.RawMessage(output)
		}
	}
	return outputs, rows.Err()
}

func (s *deploymentInfoStore) UpdateDepConfig(ctx context.Context, cfg *domain.DependencyDeployment) error {
	providerInputsJSON, err := json.Marshal(cfg.ProviderInputs)
	if err != nil {
		return fmt.Errorf("marshal provider inputs: %w", err)
	}
	userConfigJSON, err := json.Marshal(cfg.UserConfig)
	if err != nil {
		return fmt.Errorf("marshal user config: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE dependency_deployments
		    SET managed = $1, provider_inputs = $2, user_config = $3, status = $4, updated_at = NOW()
		  WHERE deployment_id = $5 AND dependency_name = $6`,
		cfg.Managed, providerInputsJSON, userConfigJSON, cfg.Status, cfg.DeploymentId, cfg.DependencyName,
	)
	if err != nil {
		return fmt.Errorf("update dep config: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) SetDepConfigCommand(ctx context.Context, deploymentID uuid.UUID, depName string, commandID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE dependency_deployments
		    SET command_id = $1, status = $2, updated_at = NOW()
		  WHERE deployment_id = $3 AND dependency_name = $4`,
		commandID, domain.DependencyDeploymentStatusPending, deploymentID, depName,
	)
	if err != nil {
		return fmt.Errorf("set dep config command: %w", err)
	}
	return nil
}

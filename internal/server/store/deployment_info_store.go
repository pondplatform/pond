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
		return nil, fmt.Errorf("deployment: %w", domain.ErrNotFound)
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
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, service_id, environment_id, image_tag, service_config_snapshot,
		        status, triggered_by, helm_command_id, created_at, completed_at
		   FROM deployments WHERE service_id = $1 ORDER BY created_at DESC`,
		serviceID,
	)
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

// ── Command operations ───────────────────────────────────────────────────────

func (s *deploymentInfoStore) Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *domain.Command) error {
	payload, err := json.Marshal(cmd.Payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO commands (id, cluster_id, deployment_id, type, payload, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'queued', $6, $6)`,
		cmd.ID, clusterID, cmd.DeploymentID, cmd.Type, payload, cmd.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue command: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) Dequeue(ctx context.Context, clusterID uuid.UUID) (*domain.Command, error) {
	var cmd domain.Command
	var payload []byte
	err := s.db.QueryRowContext(ctx,
		`UPDATE commands SET status='dispatched', updated_at=NOW()
		  WHERE id = (
		        SELECT id FROM commands
		         WHERE cluster_id = $1
		           AND status = 'queued'
		         ORDER BY created_at ASC
		         LIMIT 1
		         FOR UPDATE SKIP LOCKED
		  )
		  RETURNING id, cluster_id, deployment_id, type, payload, created_at, updated_at`,
		clusterID,
	).Scan(&cmd.ID, &cmd.ClusterID, &cmd.DeploymentID, &cmd.Type, &payload, &cmd.CreatedAt, &cmd.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dequeue command: %w", err)
	}
	cmd.Payload = payload
	cmd.Status = domain.CommandStatusDispatched
	return &cmd, nil
}

func (s *deploymentInfoStore) MarkCommandSucceeded(ctx context.Context, commandID uuid.UUID, output json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='succeeded', output=$2, updated_at=$3 WHERE id = $1`,
		commandID, []byte(output), time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark command succeeded: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) MarkCommandFailed(ctx context.Context, commandID uuid.UUID, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='failed', error=$2, updated_at=$3 WHERE id = $1`,
		commandID, errMsg, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark command failed: %w", err)
	}
	return nil
}

// Requeue transitions a dispatched command back to queued. The AND status guard
// ensures already-completed commands cannot be re-queued by a late disconnect.
func (s *deploymentInfoStore) Requeue(ctx context.Context, commandID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='queued', updated_at=NOW()
		  WHERE id = $1 AND status = 'dispatched'`,
		commandID,
	)
	if err != nil {
		return fmt.Errorf("requeue command: %w", err)
	}
	return nil
}

// CancelDeployment transitions all queued commands for a deployment to cancelled.
// Already-dispatched commands are left untouched.
func (s *deploymentInfoStore) CancelDeployment(ctx context.Context, deploymentID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE commands SET status='cancelled', updated_at=NOW()
		  WHERE deployment_id = $1 AND status = 'queued'`,
		deploymentID,
	)
	if err != nil {
		return fmt.Errorf("cancel deployment commands: %w", err)
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

// ── Dependency config operations ─────────────────────────────────────────────

func (s *deploymentInfoStore) CreateDepConfig(ctx context.Context, deploymentID uuid.UUID, cfg *domain.DeploymentDependencyConfig) error {
	userConfig, err := json.Marshal(cfg.UserConfig)
	if err != nil {
		return fmt.Errorf("marshal user config: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dependency_deployments
		        (id, deployment_id, dependency_name, dependency_type, managed, user_config, status, command_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		cfg.ID, deploymentID, cfg.DependencyName, cfg.DependencyType, cfg.Managed, userConfig, cfg.Status, cfg.CommandID,
	)
	if err != nil {
		return fmt.Errorf("create dep config: %w", err)
	}
	return nil
}

func (s *deploymentInfoStore) GetDepConfigByCommandID(ctx context.Context, commandID uuid.UUID) (uuid.UUID, *domain.DeploymentDependencyConfig, error) {
	var cfg domain.DeploymentDependencyConfig
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
		domain.DependencyRequestStatusSucceeded, []byte(output), deploymentID, depName,
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
		domain.DependencyRequestStatusFailed, deploymentID, depName,
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
		domain.DependencyRequestStatusSucceeded,
		domain.DependencyRequestStatusFailed,
	).Scan(&total, &succeeded, &failed)
	if err != nil {
		return false, false, fmt.Errorf("count dep config statuses: %w", err)
	}
	return total > 0 && succeeded == total, failed > 0, nil
}

func (s *deploymentInfoStore) GetDepOutputsByDeployment(ctx context.Context, deploymentID uuid.UUID) (map[string]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dependency_name, output FROM dependency_deployments
		  WHERE deployment_id = $1 AND status = $2 AND managed = true`,
		deploymentID, domain.DependencyRequestStatusSucceeded,
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

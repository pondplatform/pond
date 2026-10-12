package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

const dependencyFailureReason = "dependency provisioning failed"

type stateMachine struct {
	deploymentInfo store.DeploymentInfoStore
	envs           store.EnvironmentRepository
	dependencies   dependencyService
	helmGen        helmgen.HelmValuesGenerator
	tmplRenderer   TemplateRenderer
	tx             Transactor
	topics         events.Topics
	log            *slog.Logger
}

// InitializeDeployment schedules the dependencies that need provisioning and,
// when none of them waits on a tofu.apply, advances the deployment right away.
func (m *stateMachine) InitializeDeployment(ctx context.Context, deployment db.Deployment, dependencies []db.DependencyDeployment) error {
	switch aggregateStatus(dependencies) {
	case db.DependencyDeploymentStatusAwaitingInput:
		return m.setStatus(ctx, deployment.ID, api.DeploymentStatusAwaitingInput)
	case db.DependencyDeploymentStatusFailed:
		return m.fail(ctx, deployment.ID, dependencyFailureReason)
	}

	commands, err := m.loadCommands(ctx, dependencies)
	if err != nil {
		return err
	}
	toSchedule := dependenciesToSchedule(dependencies, commands)

	if err := m.setStatus(ctx, deployment.ID, api.DeploymentStatusPending); err != nil {
		return err
	}
	if err := m.scheduleAll(ctx, deployment, toSchedule); err != nil {
		return err
	}
	// Advancing here as well would race the last tofu.apply result and could
	// start helm twice.
	if slices.ContainsFunc(toSchedule, isManaged) {
		return nil
	}
	return m.advance(ctx, deployment)
}

func (m *stateMachine) AdvanceOnCommandResult(ctx context.Context, result events.CommandResult) error {
	cmd, err := m.recordResult(ctx, result)
	if err != nil {
		return err
	}
	switch cmd.Type {
	case db.CommandTypeTofuApply:
		return m.advanceOnTofuResult(ctx, cmd)
	case db.CommandTypeHelmUpgrade:
		return m.advanceOnHelmResult(ctx, cmd)
	default:
		m.log.Warn("unmatched command type", "type", cmd.Type, "command_id", cmd.ID)
		return nil
	}
}

// loadCommands returns the current command of each dependency that has one,
// keyed by command ID.
func (m *stateMachine) loadCommands(ctx context.Context, dependencies []db.DependencyDeployment) (map[uuid.UUID]*db.Command, error) {
	commands := make(map[uuid.UUID]*db.Command)
	for _, dependency := range dependencies {
		if dependency.CommandID == nil {
			continue
		}
		cmd, err := m.deploymentInfo.GetCommand(ctx, *dependency.CommandID)
		if errors.Is(err, api.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get command of dependency %q: %w", dependency.DependencyName, err)
		}
		commands[*dependency.CommandID] = cmd
	}
	return commands, nil
}

// dependenciesToSchedule picks the pending dependencies with no command in
// flight. Unmanaged ones come first: they complete on the spot, and must do so
// before a tofu.apply result can advance the deployment.
func dependenciesToSchedule(dependencies []db.DependencyDeployment, commands map[uuid.UUID]*db.Command) []db.DependencyDeployment {
	var unmanaged, managed []db.DependencyDeployment
	for _, dependency := range dependencies {
		if dependency.Status != db.DependencyDeploymentStatusPending || hasLiveCommand(dependency, commands) {
			continue
		}
		if isManaged(dependency) {
			managed = append(managed, dependency)
		} else {
			unmanaged = append(unmanaged, dependency)
		}
	}
	return append(unmanaged, managed...)
}

func hasLiveCommand(dependency db.DependencyDeployment, commands map[uuid.UUID]*db.Command) bool {
	if dependency.CommandID == nil {
		return false
	}
	cmd := commands[*dependency.CommandID]
	return cmd != nil && (cmd.Status == db.CommandStatusQueued || cmd.Status == db.CommandStatusDispatched)
}

func isManaged(dependency db.DependencyDeployment) bool {
	return dependency.Managed != nil && *dependency.Managed
}

func (m *stateMachine) scheduleAll(ctx context.Context, deployment db.Deployment, dependencies []db.DependencyDeployment) error {
	for _, dependency := range dependencies {
		if err := m.dependencies.ScheduleCommand(ctx, deployment, dependency); err != nil {
			return err
		}
	}
	return nil
}

func (m *stateMachine) recordResult(ctx context.Context, result events.CommandResult) (*db.Command, error) {
	cmd, err := m.deploymentInfo.GetCommand(ctx, result.CommandID)
	if err != nil {
		return nil, fmt.Errorf("get command: %w", err)
	}
	applyResult(cmd, result)
	if err := m.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		return nil, fmt.Errorf("update command: %w", err)
	}
	return cmd, nil
}

func applyResult(cmd *db.Command, result events.CommandResult) {
	if result.Success {
		cmd.Status = db.CommandStatusSucceeded
		cmd.Output = result.Output
	} else {
		cmd.Status = db.CommandStatusFailed
		cmd.Error = result.Error
	}
	cmd.UpdatedAt = time.Now()
}

func (m *stateMachine) advanceOnTofuResult(ctx context.Context, cmd *db.Command) error {
	found, err := m.recordDependencyOutcome(ctx, cmd)
	if err != nil || !found {
		return err
	}
	deployment, err := m.deploymentInfo.GetByID(ctx, cmd.DeploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}
	return m.advance(ctx, *deployment)
}

// recordDependencyOutcome copies a finished tofu.apply onto the dependency it
// provisions. It reports false when no dependency owns the command.
func (m *stateMachine) recordDependencyOutcome(ctx context.Context, cmd *db.Command) (bool, error) {
	_, dependency, err := m.deploymentInfo.GetDepConfigByCommandID(ctx, cmd.ID)
	if err != nil {
		return false, fmt.Errorf("get dependency of command: %w", err)
	}
	if dependency == nil {
		return false, nil
	}
	if cmd.Status == db.CommandStatusFailed {
		return true, m.failDependency(ctx, cmd.DeploymentID, dependency.DependencyName)
	}
	if err := m.deploymentInfo.MarkDepConfigSucceeded(ctx, cmd.DeploymentID, dependency.DependencyName, cmd.Output); err != nil {
		return true, fmt.Errorf("mark dependency %q succeeded: %w", dependency.DependencyName, err)
	}
	return true, nil
}

// failDependency also cancels the deployment's other queued commands: the
// deployment fails either way, so they would only waste agent time.
func (m *stateMachine) failDependency(ctx context.Context, deploymentID uuid.UUID, name string) error {
	if err := m.deploymentInfo.MarkDepConfigFailed(ctx, deploymentID, name); err != nil {
		return fmt.Errorf("mark dependency %q failed: %w", name, err)
	}
	if err := m.deploymentInfo.UpdateCommandsByDeployment(ctx, deploymentID, db.CommandStatusQueued, db.CommandStatusCancelled); err != nil {
		return fmt.Errorf("cancel queued sibling commands: %w", err)
	}
	return nil
}

// advance starts helm once every dependency has succeeded. It never schedules
// dependencies, so a command result cannot restart provisioning.
func (m *stateMachine) advance(ctx context.Context, deployment db.Deployment) error {
	dependencies, err := m.deploymentInfo.ListDepConfigs(ctx, deployment.ID)
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}
	switch aggregateStatus(dependencies) {
	case db.DependencyDeploymentStatusSucceeded:
		return m.startHelm(ctx, deployment)
	case db.DependencyDeploymentStatusFailed:
		return m.fail(ctx, deployment.ID, dependencyFailureReason)
	default:
		return nil
	}
}

func (m *stateMachine) advanceOnHelmResult(ctx context.Context, cmd *db.Command) error {
	deployment, err := m.deploymentInfo.GetByID(ctx, cmd.DeploymentID)
	if errors.Is(err, api.ErrNotFound) {
		m.log.Warn("deployment not found for helm command", "deployment_id", cmd.DeploymentID, "command_id", cmd.ID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	m.log.Info("helm upgrade completed", "deployment_id", deployment.ID, "status", cmd.Status)
	if cmd.Status == db.CommandStatusFailed {
		return m.fail(ctx, deployment.ID, cmd.Error)
	}
	now := time.Now()
	return m.deploymentInfo.UpdateStatus(ctx, deployment.ID, api.DeploymentStatusSucceeded, &now)
}

func (m *stateMachine) startHelm(ctx context.Context, deployment db.Deployment) error {
	env, err := m.envs.GetByID(ctx, deployment.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}
	contexts, err := m.dependencyContexts(ctx, deployment.ID)
	if err != nil {
		return err
	}
	cmd, err := m.newHelmUpgradeCommand(deployment, *env, contexts)
	if err != nil {
		return err
	}
	return m.enqueueHelmUpgrade(ctx, cmd)
}

func (m *stateMachine) dependencyContexts(ctx context.Context, deploymentID uuid.UUID) (map[string]map[string]any, error) {
	rawOutputs, err := m.deploymentInfo.GetDepOutputsByDeployment(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get dependency outputs: %w", err)
	}
	return buildContexts(rawOutputs)
}

func (m *stateMachine) newHelmUpgradeCommand(deployment db.Deployment, env db.Environment, contexts map[string]map[string]any) (*db.Command, error) {
	cfg, err := renderConfigs(m.tmplRenderer, deployment.ServiceConfigSnapshot, contexts)
	if err != nil {
		return nil, err
	}
	values, err := m.helmValuesYAML(&cfg, &env, deployment.ImageTag)
	if err != nil {
		return nil, err
	}
	payload, err := wire.MarshalPayload(wire.HelmUpgradePayload{
		ReleaseName: cfg.Name,
		Namespace:   env.Namespace,
		ChartPath:   "/opt/pond/helm-charts/base-service",
		Values:      values,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal helm upgrade payload: %w", err)
	}
	return newCommand(env.ClusterID, deployment.ID, db.CommandTypeHelmUpgrade, payload), nil
}

func (m *stateMachine) helmValuesYAML(cfg *serviceconfig.ServiceConfig, env *db.Environment, imageTag string) ([]byte, error) {
	values, err := m.helmGen.Generate(cfg, env, imageTag)
	if err != nil {
		return nil, fmt.Errorf("generate helm values: %w", err)
	}
	out, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal helm values: %w", err)
	}
	return out, nil
}

// renderConfigs substitutes dependency outputs into every config file's values.
func renderConfigs(renderer TemplateRenderer, cfg serviceconfig.ServiceConfig, contexts map[string]map[string]any) (serviceconfig.ServiceConfig, error) {
	if len(cfg.Configs) == 0 {
		return cfg, nil
	}
	rendered := make(map[string]serviceconfig.ConfigFileSpec, len(cfg.Configs))
	for name, file := range cfg.Configs {
		values, err := renderer.Render(file.Values, contexts, &cfg)
		if err != nil {
			return cfg, fmt.Errorf("render config %q: %w", name, err)
		}
		file.Values = values
		rendered[name] = file
	}
	cfg.Configs = rendered
	return cfg, nil
}

// enqueueHelmUpgrade records the command on the deployment in the same
// transaction that creates it, so the deployment never points at a missing
// command.
func (m *stateMachine) enqueueHelmUpgrade(ctx context.Context, cmd *db.Command) error {
	err := m.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
			return fmt.Errorf("create helm command: %w", err)
		}
		if err := tx.DeploymentInfo.SetHelmCommandID(ctx, cmd.DeploymentID, cmd.ID); err != nil {
			return fmt.Errorf("set helm command id: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return notifyCommandQueued(ctx, m.topics, cmd)
}

func (m *stateMachine) setStatus(ctx context.Context, deploymentID uuid.UUID, status db.DeploymentStatus) error {
	if err := m.deploymentInfo.UpdateStatus(ctx, deploymentID, status, nil); err != nil {
		return fmt.Errorf("set deployment %s status: %w", status, err)
	}
	return nil
}

func (m *stateMachine) fail(ctx context.Context, deploymentID uuid.UUID, reason string) error {
	now := time.Now()
	if err := m.deploymentInfo.SetFailed(ctx, deploymentID, reason, &now); err != nil {
		return fmt.Errorf("set deployment failed: %w", err)
	}
	return nil
}

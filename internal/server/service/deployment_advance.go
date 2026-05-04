package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/common/serviceconfig"
	"github.com/pondplatform/pond/internal/common/wire"
	"github.com/pondplatform/pond/internal/server/events"
	"gopkg.in/yaml.v3"
)

// processResult persists the command outcome AND advances the state machine
// atomically in one transaction, eliminating the race between result
// persistence and state read.
func (s *deploymentService) processResult(ctx context.Context, result events.CommandResult) error {
	var helmCmd *domain.Command
	err := s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		cmd, err := tx.DeploymentInfo.GetCommand(ctx, result.CommandID)
		if err != nil {
			return fmt.Errorf("get command: %w", err)
		}
		if result.Success {
			cmd.Status = domain.CommandStatusSucceeded
			cmd.Output = result.Output
		} else {
			cmd.Status = domain.CommandStatusFailed
			cmd.Error = result.Error
		}
		cmd.UpdatedAt = time.Now()
		if err := tx.DeploymentInfo.UpdateCommand(ctx, cmd); err != nil {
			return fmt.Errorf("update command: %w", err)
		}
		helmCmd, err = s.advance(ctx, tx, cmd, result)
		return err
	})
	if err != nil {
		return err
	}
	if helmCmd != nil {
		s.bus.Publish(ctx, events.ClusterCommandQueuedTopic(helmCmd.ClusterID), events.CommandQueued{
			ClusterID:    helmCmd.ClusterID,
			CommandID:    helmCmd.ID,
			DeploymentID: helmCmd.DeploymentID,
		})
	}
	return nil
}

// advance routes a command result to the appropriate handler within an
// existing transaction, dispatching by command type.
// Returns the helm command if one was enqueued as a result of this advance.
func (s *deploymentService) advance(ctx context.Context, tx TxRepos, cmd *domain.Command, result events.CommandResult) (*domain.Command, error) {
	switch cmd.Type {
	case domain.CommandTypeTofuApply:
		return s.advanceTofuApply(ctx, tx, result)
	case domain.CommandTypeHelmUpgrade:
		return nil, s.advanceHelmUpgrade(ctx, tx, cmd, result)
	default:
		s.log.Warn("unmatched command type", "type", cmd.Type, "command_id", result.CommandID)
		return nil, nil
	}
}

// advanceTofuApply handles the completion of a tofu.apply command.
// Returns the helm command if one was enqueued as a result.
func (s *deploymentService) advanceTofuApply(ctx context.Context, tx TxRepos, result events.CommandResult) (*domain.Command, error) {
	deploymentID, cfg, err := s.deploymentInfo.GetDepConfigByCommandID(ctx, result.CommandID)
	if err != nil {
		return nil, fmt.Errorf("get dep config by command id: %w", err)
	}
	if cfg == nil {
		s.log.Warn("tofu.apply command has no dep config row", "command_id", result.CommandID)
		return nil, nil
	}

	allComplete, err := s.depSvc.AdvanceOnResult(ctx, tx, deploymentID, cfg, result.Success, result.Output)
	if err != nil {
		return nil, err
	}

	if !result.Success {
		// Dependency failed - mark deployment as failed
		now := time.Now()
		return nil, tx.DeploymentInfo.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusFailed, &now)
	}

	if !allComplete {
		return nil, nil
	}

	// All deps complete - enqueue helm
	dep, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	return s.enqueueHelm(ctx, tx, dep, env)
}

// advanceHelmUpgrade handles the completion of a helm.upgrade command.
func (s *deploymentService) advanceHelmUpgrade(ctx context.Context, tx TxRepos, cmd *domain.Command, result events.CommandResult) error {
	dep, err := s.deploymentInfo.GetByID(ctx, cmd.DeploymentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("deployment not found for helm command", "deployment_id", cmd.DeploymentID, "command_id", result.CommandID)
			return nil
		}
		return fmt.Errorf("get deployment: %w", err)
	}

	now := time.Now()
	status := domain.DeploymentStatusSucceeded
	if !result.Success {
		status = domain.DeploymentStatusFailed
	}
	s.log.Info("helm upgrade completed", "deployment_id", dep.ID, "status", status)
	return tx.DeploymentInfo.UpdateStatus(ctx, dep.ID, status, &now)
}

// enqueueHelm builds helm values and creates the upgrade command inside the
// provided transaction, so the command creation and the helm_command_id update
// are atomic with the AllDepConfigsComplete check that called us.
// Returns the created helm command so the caller can publish CommandQueued after commit.
func (s *deploymentService) enqueueHelm(ctx context.Context, tx TxRepos, dep *domain.Deployment, env *domain.Environment) (*domain.Command, error) {
	rawOutputs, err := tx.DeploymentInfo.GetDepOutputsByDeployment(ctx, dep.ID)
	if err != nil {
		return nil, fmt.Errorf("get dependency outputs: %w", err)
	}

	contexts, err := s.depSvc.BuildContexts(rawOutputs)
	if err != nil {
		return nil, fmt.Errorf("build contexts: %w", err)
	}

	// Render template variables in config file values
	renderedCfg := dep.ServiceConfigSnapshot
	if len(renderedCfg.Configs) > 0 {
		renderedConfigs := make(map[string]serviceconfig.ConfigFileSpec, len(renderedCfg.Configs))
		for name, cfgFile := range renderedCfg.Configs {
			rendered, err := s.tmplRenderer.Render(cfgFile.Values, contexts, &renderedCfg)
			if err != nil {
				return nil, fmt.Errorf("render config %q: %w", name, err)
			}
			cfgFile.Values = rendered
			renderedConfigs[name] = cfgFile
		}
		renderedCfg.Configs = renderedConfigs
	}

	helmVals, err := s.helmGen.Generate(&renderedCfg, env, contexts)
	if err != nil {
		return nil, fmt.Errorf("generate helm values: %w", err)
	}
	helmValuesYAML, err := yaml.Marshal(helmVals)
	if err != nil {
		return nil, fmt.Errorf("marshal helm values: %w", err)
	}
	helmPayload := wire.HelmUpgradePayload{
		ReleaseName: dep.ServiceConfigSnapshot.Name,
		Namespace:   env.Name,
		ChartPath:   "/opt/pond/helm-charts/base-service",
		Values:      helmValuesYAML,
	}
	payload, err := wire.MarshalPayload(helmPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal helm upgrade payload: %w", err)
	}

	now := time.Now()
	cmd := &domain.Command{
		ID:           uuid.New(),
		ClusterID:    env.ClusterID,
		DeploymentID: dep.ID,
		Type:         domain.CommandTypeHelmUpgrade,
		Payload:      payload,
		Status:       domain.CommandStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
		return nil, fmt.Errorf("create helm command: %w", err)
	}
	if err := tx.DeploymentInfo.SetHelmCommandID(ctx, dep.ID, cmd.ID); err != nil {
		return nil, fmt.Errorf("set helm command id: %w", err)
	}
	dep.HelmCommandID = &cmd.ID // update in-memory struct for post-tx event publishing
	return cmd, nil
}

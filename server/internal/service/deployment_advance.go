package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

// processResult persists the command outcome AND advances the state machine
// atomically in one transaction, eliminating the race between result
// persistence and state read.
func (s *deploymentService) processResult(ctx context.Context, result events.CommandResult) error {
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
		err = s.advance(ctx, tx, cmd, result)
		return err
	})
	if err != nil {
		return err
	}
	return nil
}

// advance routes a command result to the appropriate handler within an
// existing transaction, dispatching by command type.
// Returns the helm command if one was enqueued as a result of this advance.
func (s *deploymentService) advance(ctx context.Context, tx TxRepos, cmd *domain.Command, result events.CommandResult) error {
	switch cmd.Type {
	case domain.CommandTypeTofuApply:
		return s.advanceTofuApply(ctx, tx, result)
	case domain.CommandTypeHelmUpgrade:
		return s.advanceHelmUpgrade(ctx, tx, cmd, result)
	default:
		s.log.Warn("unmatched command type", "type", cmd.Type, "command_id", result.CommandID)
		return nil
	}
}

// advanceTofuApply handles the completion of a tofu.apply command.
func (s *deploymentService) advanceTofuApply(ctx context.Context, tx TxRepos, result events.CommandResult) error {
	err := s.depSvc.HandleCommandResult(ctx, tx, result.CommandID)
	if err != nil {
		return err
	}
	return s.advanceDependencyStatus(ctx, tx, result.DeploymentID)
}

func (s *deploymentService) advanceDependencyStatus(ctx context.Context, tx TxRepos, deploymentID uuid.UUID) error {
	deployment, err := tx.DeploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return err
	}
	environment, err := s.envs.GetByID(ctx, deployment.EnvironmentID)
	if err != nil {
		return err
	}
	status, err := s.depSvc.DependencyDeploymentStatus(ctx, tx, deploymentID)
	if err != nil {
		return err
	}
	return s.advanceOnDependencyStatus(ctx, tx, deployment, environment, status)
}

// advanceHelmUpgrade handles the completion of a helm.upgrade command.
func (s *deploymentService) advanceHelmUpgrade(ctx context.Context, tx TxRepos, cmd *domain.Command, result events.CommandResult) error {
	dep, err := s.deploymentInfo.GetByID(ctx, cmd.DeploymentID)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) {
			s.log.Warn("deployment not found for helm command", "deployment_id", cmd.DeploymentID, "command_id", result.CommandID)
			return nil
		}
		return fmt.Errorf("get deployment: %w", err)
	}

	now := time.Now()
	status := api.DeploymentStatusSucceeded
	if !result.Success {
		status = api.DeploymentStatusFailed
	}
	s.log.Info("helm upgrade completed", "deployment_id", dep.ID, "status", status)
	return tx.DeploymentInfo.UpdateStatus(ctx, dep.ID, status, &now)
}

func (s *deploymentService) advanceOnDependencyStatus(ctx context.Context, tx TxRepos, deployment *domain.Deployment, environment *domain.Environment, status domain.DependencyDeploymentStatus) error {
	if status == domain.DependencyDeploymentStatusSucceeded {
		if err := tx.DeploymentInfo.UpdateStatus(ctx, deployment.ID, api.DeploymentStatusRunning, nil); err != nil {
			return fmt.Errorf("set deployment DeploymentStatusRunning status: %w", err)
		}
		if err := s.enqueueHelm(ctx, tx, deployment, environment); err != nil {
			return err
		}
	} else if status == domain.DependencyDeploymentStatusAwaitingInput {
		if err := tx.DeploymentInfo.UpdateStatus(ctx, deployment.ID, api.DeploymentStatusAwaitingInput, nil); err != nil {
			return fmt.Errorf("set deployment DeploymentStatusAwaitingInput status: %w", err)
		}
	} else {
		if err := tx.DeploymentInfo.UpdateStatus(ctx, deployment.ID, api.DeploymentStatusRunning, nil); err != nil {
			return fmt.Errorf("set deployment awaiting_input status: %w", err)
		}
		if err := s.depSvc.ScheduleCommands(ctx, tx, deployment.ID); err != nil {
			return err
		}
	}

	return nil
}

// enqueueHelm builds helm values and creates the upgrade command inside the
// provided transaction, so the command creation and the helm_command_id update
// are atomic with the AllDepConfigsComplete check that called us.
// Returns the created helm command so the caller can publish CommandQueued after commit.
func (s *deploymentService) enqueueHelm(ctx context.Context, tx TxRepos, dep *domain.Deployment, env *domain.Environment) error {
	rawOutputs, err := tx.DeploymentInfo.GetDepOutputsByDeployment(ctx, dep.ID)
	if err != nil {
		return fmt.Errorf("get dependency outputs: %w", err)
	}

	contexts, err := s.depSvc.BuildContexts(rawOutputs)
	if err != nil {
		return fmt.Errorf("build contexts: %w", err)
	}

	// Render template variables in config file values
	renderedCfg := dep.ServiceConfigSnapshot
	if len(renderedCfg.Configs) > 0 {
		renderedConfigs := make(map[string]serviceconfig.ConfigFileSpec, len(renderedCfg.Configs))
		for name, cfgFile := range renderedCfg.Configs {
			rendered, err := s.tmplRenderer.Render(cfgFile.Values, contexts, &renderedCfg)
			if err != nil {
				return fmt.Errorf("render config %q: %w", name, err)
			}
			cfgFile.Values = rendered
			renderedConfigs[name] = cfgFile
		}
		renderedCfg.Configs = renderedConfigs
	}

	helmVals, err := s.helmGen.Generate(&renderedCfg, env, contexts)
	if err != nil {
		return fmt.Errorf("generate helm values: %w", err)
	}
	helmValuesYAML, err := yaml.Marshal(helmVals)
	if err != nil {
		return fmt.Errorf("marshal helm values: %w", err)
	}
	helmPayload := wire.HelmUpgradePayload{
		ReleaseName: dep.ServiceConfigSnapshot.Name,
		Namespace:   env.Namespace,
		ChartPath:   "/opt/pond/helm-charts/base-service",
		Values:      helmValuesYAML,
	}
	payload, err := wire.MarshalPayload(helmPayload)
	if err != nil {
		return fmt.Errorf("marshal helm upgrade payload: %w", err)
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
	if err := tx.DeploymentInfo.SetHelmCommandID(ctx, dep.ID, cmd.ID); err != nil {
		return fmt.Errorf("set helm command id: %w", err)
	}

	return s.launchCommand(ctx, tx, cmd, dep.ID)
}

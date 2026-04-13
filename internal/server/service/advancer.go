package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/store"
)

type deploymentAdvancer struct {
	deployments store.DeploymentRepository
	envs        store.EnvironmentRepository
	depConfigs  store.DependencyConfigRepository
	depRequests DependencyDeploymentRequestRepository
	helmGen     helmgen.HelmValuesGenerator
	queue       CommandQueue
}

func NewDeploymentAdvancer(
	deployments store.DeploymentRepository,
	envs store.EnvironmentRepository,
	depConfigs store.DependencyConfigRepository,
	depRequests DependencyDeploymentRequestRepository,
	helmGen helmgen.HelmValuesGenerator,
	queue CommandQueue,
) DeploymentAdvancer {
	return &deploymentAdvancer{
		deployments: deployments,
		envs:        envs,
		depConfigs:  depConfigs,
		depRequests: depRequests,
		helmGen:     helmGen,
		queue:       queue,
	}
}

// Advance routes a command result to the appropriate handler.
func (a *deploymentAdvancer) Advance(ctx context.Context, result *CommandResult) error {
	// Branch 1: is this the result of a tofu.apply for a managed dependency?
	req, err := a.depRequests.GetByCommandID(ctx, result.CommandID)
	if err != nil {
		return fmt.Errorf("get dependency request by command id: %w", err)
	}
	if req != nil {
		return a.advanceDependency(ctx, req, result)
	}

	// Branch 2: is this the result of a helm.upgrade?
	dep, err := a.deployments.GetByHelmCommandID(ctx, result.CommandID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			log.Printf("advance: no deployment for helm command %s (may be cancelled)", result.CommandID)
			return nil
		}
		return fmt.Errorf("get deployment by helm command id: %w", err)
	}
	if dep != nil {
		return a.advanceHelm(ctx, dep, result)
	}

	log.Printf("advance: unmatched command result %s", result.CommandID)
	return nil
}

func (a *deploymentAdvancer) advanceDependency(ctx context.Context, req *domain.DependencyDeploymentRequest, result *CommandResult) error {
	if !result.Success {
		if err := a.depRequests.MarkFailed(ctx, req.CommandID); err != nil {
			log.Printf("mark dependency request failed: %v", err)
		}
		if err := a.queue.CancelDeployment(ctx, req.DeploymentID); err != nil {
			log.Printf("cancel sibling commands for deployment %s: %v", req.DeploymentID, err)
		}
		now := time.Now()
		return a.deployments.UpdateStatus(ctx, req.DeploymentID, domain.DeploymentStatusFailed, &now)
	}

	if err := a.depRequests.MarkSucceeded(ctx, req.CommandID, result.Output); err != nil {
		return fmt.Errorf("mark dependency request succeeded: %w", err)
	}

	allSucceeded, anyFailed, err := a.depRequests.AllComplete(ctx, req.DeploymentID)
	if err != nil {
		return fmt.Errorf("check all complete: %w", err)
	}
	if anyFailed {
		return nil
	}
	if !allSucceeded {
		return nil
	}

	return a.enqueueHelmForDeployment(ctx, req.DeploymentID)
}

func (a *deploymentAdvancer) advanceHelm(ctx context.Context, dep *domain.Deployment, result *CommandResult) error {
	now := time.Now()
	status := domain.DeploymentStatusSucceeded
	if !result.Success {
		status = domain.DeploymentStatusFailed
	}
	return a.deployments.UpdateStatus(ctx, dep.ID, status, &now)
}

// MarkDispatched transitions a deployment from pending → running when its
// first command has been sent to the agent.
func (a *deploymentAdvancer) MarkDispatched(ctx context.Context, deploymentID uuid.UUID) error {
	dep, err := a.deployments.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment for mark dispatched: %w", err)
	}
	if dep.Status != domain.DeploymentStatusPending {
		return nil
	}
	return a.deployments.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusRunning, nil)
}

func (a *deploymentAdvancer) enqueueHelmForDeployment(ctx context.Context, deploymentID uuid.UUID) error {
	dep, err := a.deployments.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	env, err := a.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}

	rawOutputs, err := a.depRequests.GetOutputsByDeployment(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get dependency outputs: %w", err)
	}

	contexts := make(map[string]domain.ResolvedContext, len(dep.ServiceConfigSnapshot.Dependencies))
	for depName := range dep.ServiceConfigSnapshot.Dependencies {
		if raw, ok := rawOutputs[depName]; ok {
			var vals map[string]any
			if err := json.Unmarshal(raw, &vals); err != nil {
				return fmt.Errorf("unmarshal tofu outputs for %q: %w", depName, err)
			}
			contexts[depName] = domain.ResolvedContext{DependencyName: depName, Values: vals}
			continue
		}
		cfg, err := a.depConfigs.Get(ctx, dep.ServiceID, dep.EnvironmentID, depName)
		if err != nil {
			return fmt.Errorf("get dep config for %q: %w", depName, err)
		}
		contexts[depName] = domain.ResolvedContext{DependencyName: depName, Values: cfg.UserConfig}
	}

	helmVals, err := a.helmGen.Generate(&dep.ServiceConfigSnapshot, env, contexts)
	if err != nil {
		return fmt.Errorf("generate helm values: %w", err)
	}
	payload, err := json.Marshal(helmVals)
	if err != nil {
		return fmt.Errorf("marshal helm values: %w", err)
	}

	cmd := &Command{
		ID:           uuid.New(),
		DeploymentID: dep.ID,
		Type:         "helm.upgrade",
		Payload:      payload,
		CreatedAt:    time.Now(),
	}
	if err := a.queue.Enqueue(ctx, env.ClusterID, cmd); err != nil {
		return fmt.Errorf("enqueue helm command: %w", err)
	}
	return a.deployments.SetHelmCommandID(ctx, dep.ID, cmd.ID)
}

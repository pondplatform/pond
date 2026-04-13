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
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/store"
)

type deploymentService struct {
	deploymentInfo store.DeploymentInfoStore
	services       store.ServiceRepository
	envs           store.EnvironmentRepository
	depSvc         DependencyService
	helmGen        helmgen.HelmValuesGenerator
	tx             Transactor
	bus            events.Bus
}

func NewDeploymentService(
	deploymentInfo store.DeploymentInfoStore,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	depSvc DependencyService,
	helmGen helmgen.HelmValuesGenerator,
	tx Transactor,
	bus events.Bus,
) DeploymentService {
	return &deploymentService{
		deploymentInfo: deploymentInfo,
		services:       services,
		envs:           envs,
		depSvc:         depSvc,
		helmGen:        helmGen,
		tx:             tx,
		bus:            bus,
	}
}

func (s *deploymentService) Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error) {
	// --- reads (outside the transaction) ---

	svc, err := s.services.GetByName(ctx, req.ProjectID, req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("lookup service %q: %w", req.ServiceConfig.Name, err)
	}

	env, err := s.envs.GetByID(ctx, req.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("lookup environment: %w", err)
	}

	d := &domain.Deployment{
		ID:                    uuid.New(),
		ServiceID:             svc.ID,
		EnvironmentID:         env.ID,
		ImageTag:              req.ImageTag,
		ServiceConfigSnapshot: req.ServiceConfig,
		Status:                domain.DeploymentStatusPending,
		TriggeredBy:           req.TriggeredBy,
		CreatedAt:             time.Now(),
	}

	// --- writes (all in one transaction) ---

	var pendingDeps []PendingDep
	var helmCmd *domain.Command

	err = s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		if err := tx.DeploymentInfo.Create(ctx, d); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}

		var schedErr error
		pendingDeps, schedErr = s.depSvc.ScheduleCommands(ctx, tx, d, env.ClusterID)
		if schedErr != nil {
			return schedErr
		}

		if len(pendingDeps) == 0 {
			// No managed deps: resolve contexts and enqueue helm now.
			contexts, err := s.depSvc.BuildContexts(ctx, d, nil)
			if err != nil {
				return fmt.Errorf("build contexts: %w", err)
			}
			helmVals, err := s.helmGen.Generate(&d.ServiceConfigSnapshot, env, contexts)
			if err != nil {
				return fmt.Errorf("generate helm values: %w", err)
			}
			payload, err := json.Marshal(helmVals)
			if err != nil {
				return fmt.Errorf("marshal helm values: %w", err)
			}
			helmCmd = &domain.Command{
				ID:           uuid.New(),
				DeploymentID: d.ID,
				Type:         "helm.upgrade",
				Payload:      payload,
				CreatedAt:    time.Now(),
			}
			if err := tx.DeploymentInfo.Enqueue(ctx, env.ClusterID, helmCmd); err != nil {
				return fmt.Errorf("enqueue helm command: %w", err)
			}
			if err := tx.DeploymentInfo.SetHelmCommandID(ctx, d.ID, helmCmd.ID); err != nil {
				return fmt.Errorf("set helm command id: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Notify any idle connected agent for this cluster that commands are ready.
	if len(pendingDeps) > 0 {
		for _, p := range pendingDeps {
			s.bus.Publish(ctx, events.ClusterTopic(env.ClusterID), events.CommandQueued{
				ClusterID:    env.ClusterID,
				CommandID:    p.Cmd.ID,
				DeploymentID: d.ID,
			})
		}
	} else {
		s.bus.Publish(ctx, events.ClusterTopic(env.ClusterID), events.CommandQueued{
			ClusterID:    env.ClusterID,
			CommandID:    helmCmd.ID,
			DeploymentID: d.ID,
		})
	}

	return d, nil
}

func (s *deploymentService) GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error) {
	d, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return d, nil
}

func (s *deploymentService) Validate(ctx context.Context, req SubmitRequest) (*ValidationResult, error) {
	result := &ValidationResult{Valid: true}

	_, err := s.services.GetByName(ctx, req.ProjectID, req.ServiceConfig.Name)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, domain.ValidationError{
			Component: "service",
			Field:     "name",
			Message:   fmt.Sprintf("service %q not found", req.ServiceConfig.Name),
		})
	}

	_, err = s.envs.GetByID(ctx, req.EnvironmentID)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, domain.ValidationError{
			Component: "environment",
			Field:     "id",
			Message:   "environment not found",
		})
	}

	if err := s.depSvc.Validate(ctx, uuid.Nil, req.EnvironmentID, req.ServiceConfig.Dependencies); err != nil {
		result.Valid = false
		if ve, ok := err.(*domain.ValidationErrors); ok {
			result.Errors = append(result.Errors, ve.Errors...)
		}
	}

	return result, nil
}

// MarkRunning transitions a deployment from pending → running when the agent
// confirms it has begun executing the command.
func (s *deploymentService) MarkRunning(ctx context.Context, deploymentID uuid.UUID) error {
	dep, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment for mark running: %w", err)
	}
	if dep.Status != domain.DeploymentStatusPending {
		return nil
	}
	return s.deploymentInfo.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusRunning, nil)
}

// Start subscribes to the command_results topic and drives the deployment state
// machine for each result. Blocks until ctx is cancelled.
func (s *deploymentService) Start(ctx context.Context) {
	unsub := s.bus.Subscribe(events.TopicCommandResults, func(v any) {
		res, ok := v.(events.CommandResult)
		if !ok {
			log.Printf("deployment service: unexpected event type %T on command_results", v)
			return
		}
		if err := s.processResult(ctx, res); err != nil {
			log.Printf("deployment service: processResult for command %s: %v", res.CommandID, err)
		}
	})
	<-ctx.Done()
	unsub()
}

// processResult drives the deployment state machine forward based on a command
// result. Runs the state advance inside a transaction and publishes a
// CommandQueued event to the cluster topic if a new command was enqueued.
func (s *deploymentService) processResult(ctx context.Context, result events.CommandResult) error {
	var clusterToNotify *uuid.UUID

	err := s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		clusterID, err := s.advance(ctx, tx, result)
		if err != nil {
			return err
		}
		clusterToNotify = clusterID
		return nil
	})
	if err != nil {
		return err
	}

	if clusterToNotify != nil {
		s.bus.Publish(ctx, events.ClusterTopic(*clusterToNotify), events.CommandQueued{
			ClusterID: *clusterToNotify,
		})
	}
	return nil
}

// advance routes a command result to the appropriate handler within an existing
// transaction. Returns the cluster ID to notify if a new command was enqueued.
func (s *deploymentService) advance(ctx context.Context, tx TxRepos, result events.CommandResult) (*uuid.UUID, error) {
	// Branch 1: is this the result of a tofu.apply for a managed dependency?
	deploymentID, cfg, err := s.deploymentInfo.GetDepConfigByCommandID(ctx, result.CommandID)
	if err != nil {
		return nil, fmt.Errorf("get dep config by command id: %w", err)
	}
	if cfg != nil {
		return s.advanceDependency(ctx, tx, deploymentID, cfg, result)
	}

	// Branch 2: is this the result of a helm.upgrade?
	dep, err := s.deploymentInfo.GetByHelmCommandID(ctx, result.CommandID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			log.Printf("advance: no deployment for helm command %s (may be cancelled)", result.CommandID)
			return nil, nil
		}
		return nil, fmt.Errorf("get deployment by helm command id: %w", err)
	}
	if dep != nil {
		return nil, s.advanceHelm(ctx, tx, dep, result)
	}

	log.Printf("advance: unmatched command result %s", result.CommandID)
	return nil, nil
}

func (s *deploymentService) advanceDependency(ctx context.Context, tx TxRepos, deploymentID uuid.UUID, cfg *domain.DeploymentDependencyConfig, result events.CommandResult) (*uuid.UUID, error) {
	if !result.Success {
		if err := tx.DeploymentInfo.MarkDepConfigFailed(ctx, deploymentID, cfg.DependencyName); err != nil {
			return nil, fmt.Errorf("mark dep config failed: %w", err)
		}
		if err := tx.DeploymentInfo.CancelDeployment(ctx, deploymentID); err != nil {
			return nil, fmt.Errorf("cancel sibling commands for deployment %s: %w", deploymentID, err)
		}
		now := time.Now()
		return nil, tx.DeploymentInfo.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusFailed, &now)
	}

	dep, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}

	if err := tx.DeploymentInfo.MarkDepConfigSucceeded(ctx, deploymentID, cfg.DependencyName, result.Output); err != nil {
		return nil, fmt.Errorf("mark dep config succeeded: %w", err)
	}

	// AllDepConfigsComplete runs in the same tx so it sees the MarkDepConfigSucceeded
	// row above, eliminating the TOCTOU race between concurrent dependency completions.
	allSucceeded, anyFailed, err := tx.DeploymentInfo.AllDepConfigsComplete(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("check all complete: %w", err)
	}
	if anyFailed || !allSucceeded {
		return nil, nil
	}

	if err := s.enqueueHelm(ctx, tx, dep, env); err != nil {
		return nil, err
	}
	return &env.ClusterID, nil
}

func (s *deploymentService) advanceHelm(ctx context.Context, tx TxRepos, dep *domain.Deployment, result events.CommandResult) error {
	now := time.Now()
	status := domain.DeploymentStatusSucceeded
	if !result.Success {
		status = domain.DeploymentStatusFailed
	}
	return tx.DeploymentInfo.UpdateStatus(ctx, dep.ID, status, &now)
}

// enqueueHelm builds helm values and enqueues the upgrade command inside the
// provided transaction, so the enqueue and the helm_command_id update are
// atomic with the AllDepConfigsComplete check that called us.
func (s *deploymentService) enqueueHelm(ctx context.Context, tx TxRepos, dep *domain.Deployment, env *domain.Environment) error {
	rawOutputs, err := tx.DeploymentInfo.GetDepOutputsByDeployment(ctx, dep.ID)
	if err != nil {
		return fmt.Errorf("get dependency outputs: %w", err)
	}

	contexts, err := s.depSvc.BuildContexts(ctx, dep, rawOutputs)
	if err != nil {
		return fmt.Errorf("build contexts: %w", err)
	}

	helmVals, err := s.helmGen.Generate(&dep.ServiceConfigSnapshot, env, contexts)
	if err != nil {
		return fmt.Errorf("generate helm values: %w", err)
	}
	payload, err := json.Marshal(helmVals)
	if err != nil {
		return fmt.Errorf("marshal helm values: %w", err)
	}

	cmd := &domain.Command{
		ID:           uuid.New(),
		DeploymentID: dep.ID,
		Type:         "helm.upgrade",
		Payload:      payload,
		CreatedAt:    time.Now(),
	}
	if err := tx.DeploymentInfo.Enqueue(ctx, env.ClusterID, cmd); err != nil {
		return fmt.Errorf("enqueue helm command: %w", err)
	}
	return tx.DeploymentInfo.SetHelmCommandID(ctx, dep.ID, cmd.ID)
}

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
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/store"
)

type deploymentService struct {
	deployments store.DeploymentRepository
	services    store.ServiceRepository
	envs        store.EnvironmentRepository
	depConfigs  store.DependencyConfigRepository
	depRequests store.DependencyDeploymentRequestRepository
	resolver    dependency.DependencyResolver
	helmGen     helmgen.HelmValuesGenerator
	tx          Transactor
	bus         events.Bus
	commands    store.CommandRepository
}

func NewDeploymentService(
	deployments store.DeploymentRepository,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	depConfigs store.DependencyConfigRepository,
	depRequests store.DependencyDeploymentRequestRepository,
	resolver dependency.DependencyResolver,
	helmGen helmgen.HelmValuesGenerator,
	tx Transactor,
	bus events.Bus,
	commands store.CommandRepository,
) DeploymentService {
	return &deploymentService{
		deployments: deployments,
		services:    services,
		envs:        envs,
		depConfigs:  depConfigs,
		depRequests: depRequests,
		resolver:    resolver,
		helmGen:     helmGen,
		tx:          tx,
		bus:         bus,
		commands:    commands,
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

	// Build the list of managed-dependency commands before entering the tx so
	// that depConfig reads don't have to participate in the write transaction.
	type pendingDep struct {
		cmd    *domain.Command
		depReq *domain.DependencyDeploymentRequest
	}
	var pendingDeps []pendingDep

	for depName, decl := range req.ServiceConfig.Dependencies {
		cfg, err := s.depConfigs.Get(ctx, svc.ID, env.ID, depName)
		if err != nil || !cfg.Managed {
			continue
		}

		workDir, _ := cfg.ProviderInputs["workDir"].(string)
		vars := make(map[string]string, len(decl.Config))
		for k, v := range decl.Config {
			if sv, ok := v.(string); ok {
				vars[k] = sv
			}
		}
		payload, err := json.Marshal(struct {
			WorkDir string            `json:"workDir"`
			Vars    map[string]string `json:"vars"`
		}{WorkDir: workDir, Vars: vars})
		if err != nil {
			return nil, fmt.Errorf("marshal tofu payload for %q: %w", depName, err)
		}

		cmd := &domain.Command{
			ID:           uuid.New(),
			DeploymentID: d.ID,
			Type:         "tofu.apply",
			Payload:      payload,
			CreatedAt:    time.Now(),
		}
		depReq := &domain.DependencyDeploymentRequest{
			ID:             uuid.New(),
			DeploymentID:   d.ID,
			CommandID:      cmd.ID,
			DependencyName: depName,
			Status:         domain.DependencyRequestStatusPending,
		}
		pendingDeps = append(pendingDeps, pendingDep{cmd: cmd, depReq: depReq})
	}

	// For the no-managed-deps path: generate helm values before the tx so that
	// the slow helmGen call doesn't hold the transaction open.
	var helmCmd *domain.Command
	if len(pendingDeps) == 0 {
		contexts, err := s.resolver.ResolveAll(ctx, svc.ID, env.ID, req.ServiceConfig.Dependencies)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies: %w", err)
		}
		helmVals, err := s.helmGen.Generate(&d.ServiceConfigSnapshot, env, contexts)
		if err != nil {
			return nil, fmt.Errorf("generate helm values: %w", err)
		}
		payload, err := json.Marshal(helmVals)
		if err != nil {
			return nil, fmt.Errorf("marshal helm values: %w", err)
		}
		helmCmd = &domain.Command{
			ID:           uuid.New(),
			DeploymentID: d.ID,
			Type:         "helm.upgrade",
			Payload:      payload,
			CreatedAt:    time.Now(),
		}
	}

	// --- writes (all in one transaction) ---

	err = s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		if err := tx.Deployments.Create(ctx, d); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}

		if len(pendingDeps) > 0 {
			for _, p := range pendingDeps {
				if err := tx.Commands.Enqueue(ctx, env.ClusterID, p.cmd); err != nil {
					return fmt.Errorf("enqueue tofu command for dep %s: %w", p.depReq.DependencyName, err)
				}
				if err := tx.DepRequests.Create(ctx, p.depReq); err != nil {
					return fmt.Errorf("create dependency request for dep %s: %w", p.depReq.DependencyName, err)
				}
			}
		} else {
			if err := tx.Commands.Enqueue(ctx, env.ClusterID, helmCmd); err != nil {
				return fmt.Errorf("enqueue helm command: %w", err)
			}
			if err := tx.Deployments.SetHelmCommandID(ctx, d.ID, helmCmd.ID); err != nil {
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
				CommandID:    p.cmd.ID,
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
	d, err := s.deployments.GetByID(ctx, deploymentID)
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

	if err := s.resolver.Validate(ctx, uuid.Nil, req.EnvironmentID, req.ServiceConfig.Dependencies); err != nil {
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
	dep, err := s.deployments.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment for mark running: %w", err)
	}
	if dep.Status != domain.DeploymentStatusPending {
		return nil
	}
	return s.deployments.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusRunning, nil)
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
		// The agent handler calls sendNextOrIdle() directly after publishing the
		// result, so this publish primarily wakes agents that became idle between
		// the result and the helm enqueue (e.g. multi-agent edge cases).
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
	req, err := s.depRequests.GetByCommandID(ctx, result.CommandID)
	if err != nil {
		return nil, fmt.Errorf("get dependency request by command id: %w", err)
	}
	if req != nil {
		return s.advanceDependency(ctx, tx, req, result)
	}

	// Branch 2: is this the result of a helm.upgrade?
	dep, err := s.deployments.GetByHelmCommandID(ctx, result.CommandID)
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

func (s *deploymentService) advanceDependency(ctx context.Context, tx TxRepos, req *domain.DependencyDeploymentRequest, result events.CommandResult) (*uuid.UUID, error) {
	if !result.Success {
		if err := tx.DepRequests.MarkFailed(ctx, req.CommandID); err != nil {
			return nil, fmt.Errorf("mark dependency request failed: %w", err)
		}
		if err := tx.Commands.CancelDeployment(ctx, req.DeploymentID); err != nil {
			return nil, fmt.Errorf("cancel sibling commands for deployment %s: %w", req.DeploymentID, err)
		}
		now := time.Now()
		return nil, tx.Deployments.UpdateStatus(ctx, req.DeploymentID, domain.DeploymentStatusFailed, &now)
	}

	// Read data needed to potentially generate helm values. These reads happen
	// outside the outer transaction's write scope, so we use the non-tx repos.
	dep, err := s.deployments.GetByID(ctx, req.DeploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}

	if err := tx.DepRequests.MarkSucceeded(ctx, req.CommandID, result.Output); err != nil {
		return nil, fmt.Errorf("mark dependency request succeeded: %w", err)
	}

	// AllComplete runs in the same tx so it sees the MarkSucceeded row above,
	// eliminating the TOCTOU race between concurrent dependency completions.
	allSucceeded, anyFailed, err := tx.DepRequests.AllComplete(ctx, req.DeploymentID)
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
	return tx.Deployments.UpdateStatus(ctx, dep.ID, status, &now)
}

// enqueueHelm builds helm values and enqueues the upgrade command inside the
// provided transaction, so the enqueue and the helm_command_id update are
// atomic with the AllComplete check that called us.
func (s *deploymentService) enqueueHelm(ctx context.Context, tx TxRepos, dep *domain.Deployment, env *domain.Environment) error {
	rawOutputs, err := tx.DepRequests.GetOutputsByDeployment(ctx, dep.ID)
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
		cfg, err := s.depConfigs.Get(ctx, dep.ServiceID, dep.EnvironmentID, depName)
		if err != nil {
			return fmt.Errorf("get dep config for %q: %w", depName, err)
		}
		contexts[depName] = domain.ResolvedContext{DependencyName: depName, Values: cfg.UserConfig}
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
	if err := tx.Commands.Enqueue(ctx, env.ClusterID, cmd); err != nil {
		return fmt.Errorf("enqueue helm command: %w", err)
	}
	return tx.Deployments.SetHelmCommandID(ctx, dep.ID, cmd.ID)
}

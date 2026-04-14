package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/config"
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
	tmplRenderer   config.TemplateRenderer
	tx             Transactor
	bus            events.Bus
}

func NewDeploymentService(
	deploymentInfo store.DeploymentInfoStore,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	depSvc DependencyService,
	helmGen helmgen.HelmValuesGenerator,
	tmplRenderer config.TemplateRenderer,
	tx Transactor,
	bus events.Bus,
) DeploymentService {
	return &deploymentService{
		deploymentInfo: deploymentInfo,
		services:       services,
		envs:           envs,
		depSvc:         depSvc,
		helmGen:        helmGen,
		tmplRenderer:   tmplRenderer,
		tx:             tx,
		bus:            bus,
	}
}

func (s *deploymentService) Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error) {
	// --- reads (outside the transaction) ---
	// Note: Validate performs the same lookups. Submit re-fetches independently
	// because the handler does not guarantee Validate was called first, and the
	// data may have changed between the two calls.

	svc, err := s.services.GetByName(ctx, req.ProjectID, req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("lookup service %q: %w", req.ServiceConfig.Name, err)
	}

	env, err := s.envs.GetByID(ctx, req.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("lookup environment: %w", err)
	}

	// --- writes (all in one transaction) ---

	var d *domain.Deployment
	var pendingDeps []PendingDep

	err = s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		now := time.Now()
		d = &domain.Deployment{
			ID:                    uuid.New(),
			ServiceID:             svc.ID,
			EnvironmentID:         env.ID,
			ImageTag:              req.ImageTag,
			ServiceConfigSnapshot: req.ServiceConfig,
			Status:                domain.DeploymentStatusPending,
			TriggeredBy:           req.TriggeredBy,
			CreatedAt:             now,
		}

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
			if err := s.enqueueHelm(ctx, tx, d, env); err != nil {
				return err
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
			CommandID:    *d.HelmCommandID,
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

	if err := s.depSvc.Validate(ctx, req.ServiceConfig.Dependencies); err != nil {
		result.Valid = false
		if ve, ok := err.(*domain.ValidationErrors); ok {
			result.Errors = append(result.Errors, ve.Errors...)
		}
	}

	return result, nil
}

// Start subscribes to every handler→service topic and drives the deployment
// state machine in response. Blocks until ctx is cancelled.
func (s *deploymentService) Start(ctx context.Context) {
	unsubs := []func(){
		s.bus.Subscribe(events.TopicCommandResults, func(v any) {
			res, ok := v.(events.CommandResult)
			if !ok {
				log.Printf("deployment service: unexpected event type %T on command_results", v)
				return
			}
			if err := s.processResult(ctx, res); err != nil {
				log.Printf("deployment service: processResult for command %s: %v", res.CommandID, err)
			}
		}),
		s.bus.Subscribe(events.TopicAgentReady, func(v any) {
			e, ok := v.(events.AgentReady)
			if !ok {
				log.Printf("deployment service: unexpected event type %T on agent_ready", v)
				return
			}
			s.handleAgentReady(ctx, e)
		}),
		s.bus.Subscribe(events.TopicCommandStarted, func(v any) {
			e, ok := v.(events.CommandStarted)
			if !ok {
				log.Printf("deployment service: unexpected event type %T on command_started", v)
				return
			}
			s.handleCommandStarted(ctx, e)
		}),
		s.bus.Subscribe(events.TopicCommandLogs, func(v any) {
			e, ok := v.(events.CommandLog)
			if !ok {
				log.Printf("deployment service: unexpected event type %T on command_logs", v)
				return
			}
			s.handleCommandLog(ctx, e)
		}),
		s.bus.Subscribe(events.TopicAgentDisconnected, func(v any) {
			e, ok := v.(events.AgentDisconnected)
			if !ok {
				log.Printf("deployment service: unexpected event type %T on agent_disconnected", v)
				return
			}
			s.handleAgentDisconnected(ctx, e)
		}),
	}
	<-ctx.Done()
	for _, u := range unsubs {
		u()
	}
}

// handleAgentReady dispatches the next queued command for the cluster.
func (s *deploymentService) handleAgentReady(ctx context.Context, e events.AgentReady) {
	cmds, err := s.deploymentInfo.ListQueuedCommandsByCluster(ctx, e.ClusterID)
	if err != nil {
		log.Printf("deployment service: list queued commands for cluster %s: %v", e.ClusterID, err)
		return
	}
	if len(cmds) == 0 {
		return
	}
	cmd := cmds[0]
	cmd.Status = domain.CommandStatusDispatched
	cmd.UpdatedAt = time.Now()
	if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		log.Printf("deployment service: update command %s to dispatched: %v", cmd.ID, err)
		return
	}
	s.bus.Publish(ctx, events.ClusterTopic(e.ClusterID), events.CommandDispatch{Cmd: cmd})
}

// handleCommandStarted transitions a deployment from pending → running when
// the agent acknowledges the command it just received.
func (s *deploymentService) handleCommandStarted(ctx context.Context, e events.CommandStarted) {
	dep, err := s.deploymentInfo.GetByID(ctx, e.DeploymentID)
	if err != nil {
		log.Printf("deployment service: get deployment %s for command_started: %v", e.DeploymentID, err)
		return
	}
	if dep.Status != domain.DeploymentStatusPending {
		return
	}
	if err := s.deploymentInfo.UpdateStatus(ctx, e.DeploymentID, domain.DeploymentStatusRunning, nil); err != nil {
		log.Printf("deployment service: mark running %s: %v", e.DeploymentID, err)
	}
}

// handleCommandLog persists a streamed log line from a running command.
func (s *deploymentService) handleCommandLog(ctx context.Context, e events.CommandLog) {
	if err := s.deploymentInfo.AppendLog(ctx, e.CommandID, e.Line); err != nil {
		log.Printf("deployment service: append log for command %s: %v", e.CommandID, err)
	}
}

// handleAgentDisconnected requeues the in-flight command (if any) so it can
// be redelivered to the next connected agent for the same cluster.
func (s *deploymentService) handleAgentDisconnected(ctx context.Context, e events.AgentDisconnected) {
	if e.InFlightCommandID == uuid.Nil {
		return
	}
	cmd, err := s.deploymentInfo.GetCommand(ctx, e.InFlightCommandID)
	if err != nil {
		log.Printf("deployment service: get command %s on disconnect: %v", e.InFlightCommandID, err)
		return
	}
	if cmd.Status != domain.CommandStatusDispatched {
		return // already completed or cancelled
	}
	cmd.Status = domain.CommandStatusQueued
	cmd.UpdatedAt = time.Now()
	if err := s.deploymentInfo.UpdateCommand(ctx, cmd); err != nil {
		log.Printf("deployment service: requeue command %s on disconnect: %v", e.InFlightCommandID, err)
	}
}

// processResult persists the command outcome AND advances the state machine
// atomically in one transaction, eliminating the race between result
// persistence and state read.
func (s *deploymentService) processResult(ctx context.Context, result events.CommandResult) error {
	return s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
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
		return s.advance(ctx, tx, cmd, result)
	})
}

// advance routes a command result to the appropriate handler within an
// existing transaction, dispatching by command type.
func (s *deploymentService) advance(ctx context.Context, tx TxRepos, cmd *domain.Command, result events.CommandResult) error {
	switch cmd.Type {
	case "tofu.apply":
		deploymentID, cfg, err := s.deploymentInfo.GetDepConfigByCommandID(ctx, result.CommandID)
		if err != nil {
			return fmt.Errorf("get dep config by command id: %w", err)
		}
		if cfg == nil {
			log.Printf("advance: tofu.apply command %s has no dep config row", result.CommandID)
			return nil
		}
		return s.advanceDependency(ctx, tx, deploymentID, cfg, result)

	case "helm.upgrade":
		dep, err := s.deploymentInfo.GetByID(ctx, cmd.DeploymentID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				log.Printf("advance: deployment %s not found for helm command %s", cmd.DeploymentID, result.CommandID)
				return nil
			}
			return fmt.Errorf("get deployment: %w", err)
		}
		return s.advanceHelm(ctx, tx, dep, result)

	default:
		log.Printf("advance: unmatched command type %q for command %s", cmd.Type, result.CommandID)
		return nil
	}
}

func (s *deploymentService) advanceDependency(ctx context.Context, tx TxRepos, deploymentID uuid.UUID, cfg *domain.DeploymentDependencyConfig, result events.CommandResult) error {
	if !result.Success {
		if err := tx.DeploymentInfo.MarkDepConfigFailed(ctx, deploymentID, cfg.DependencyName); err != nil {
			return fmt.Errorf("mark dep config failed: %w", err)
		}
		if err := tx.DeploymentInfo.UpdateCommandsByDeployment(ctx, deploymentID, domain.CommandStatusQueued, domain.CommandStatusCancelled); err != nil {
			return fmt.Errorf("cancel sibling commands for deployment %s: %w", deploymentID, err)
		}
		now := time.Now()
		return tx.DeploymentInfo.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusFailed, &now)
	}

	dep, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}
	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}

	if err := tx.DeploymentInfo.MarkDepConfigSucceeded(ctx, deploymentID, cfg.DependencyName, result.Output); err != nil {
		return fmt.Errorf("mark dep config succeeded: %w", err)
	}

	// AllDepConfigsComplete runs in the same tx so it sees the MarkDepConfigSucceeded
	// row above, eliminating the TOCTOU race between concurrent dependency completions.
	allSucceeded, anyFailed, err := tx.DeploymentInfo.AllDepConfigsComplete(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("check all complete: %w", err)
	}
	if anyFailed || !allSucceeded {
		return nil
	}

	return s.enqueueHelm(ctx, tx, dep, env)
}

func (s *deploymentService) advanceHelm(ctx context.Context, tx TxRepos, dep *domain.Deployment, result events.CommandResult) error {
	now := time.Now()
	status := domain.DeploymentStatusSucceeded
	if !result.Success {
		status = domain.DeploymentStatusFailed
	}
	return tx.DeploymentInfo.UpdateStatus(ctx, dep.ID, status, &now)
}

// enqueueHelm builds helm values and creates the upgrade command inside the
// provided transaction, so the command creation and the helm_command_id update
// are atomic with the AllDepConfigsComplete check that called us.
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
		renderedConfigs := make(map[string]domain.ConfigFileSpec, len(renderedCfg.Configs))
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
	payload, err := json.Marshal(helmVals)
	if err != nil {
		return fmt.Errorf("marshal helm values: %w", err)
	}

	now := time.Now()
	cmd := &domain.Command{
		ID:           uuid.New(),
		ClusterID:    env.ClusterID,
		DeploymentID: dep.ID,
		Type:         "helm.upgrade",
		Payload:      payload,
		Status:       domain.CommandStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
		return fmt.Errorf("create helm command: %w", err)
	}
	if err := tx.DeploymentInfo.SetHelmCommandID(ctx, dep.ID, cmd.ID); err != nil {
		return fmt.Errorf("set helm command id: %w", err)
	}
	dep.HelmCommandID = &cmd.ID // update in-memory struct for post-tx event publishing
	return nil
}

package service

import (
	"context"
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
	var deps []domain.DependencyDeployment

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
		deps, schedErr = s.depSvc.ScheduleCommands(ctx, tx, svc, env, d)
		if schedErr != nil {
			return schedErr
		}

		// Only enqueue helm if there are no dependencies at all
		// If there are dependencies but none awaiting input, commands will be dispatched below
		if len(deps) == 0 {
			if err := s.enqueueHelm(ctx, tx, d, env); err != nil {
				return err
			}
		} else if anyDepAwaitingInput(deps) {
			// If any dependency needs input, don't enqueue anything yet.
			// Wait for all inputs to be provided before scheduling.
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Publish events based on dependency state
	s.publishSubmitEvents(ctx, deps, env, d, req.ProjectID)

	return d, nil
}

// anyDepAwaitingInput returns true if any dependency is in awaiting_input status.
func anyDepAwaitingInput(deps []domain.DependencyDeployment) bool {
	for _, dep := range deps {
		if dep.Status == domain.DependencyDeploymentStatusAwaitingInput {
			return true
		}
	}
	return false
}

// publishSubmitEvents publishes the appropriate events after a deployment is submitted.
func (s *deploymentService) publishSubmitEvents(ctx context.Context, deps []domain.DependencyDeployment, env *domain.Environment, d *domain.Deployment, projectID uuid.UUID) {
	if len(deps) == 0 {
		// No dependencies: helm command was enqueued, notify agent
		s.bus.Publish(ctx, events.ClusterCommandQueuedTopic(env.ClusterID), events.CommandQueued{
			ClusterID:    env.ClusterID,
			CommandID:    *d.HelmCommandID,
			DeploymentID: d.ID,
		})
		return
	}

	if anyDepAwaitingInput(deps) {
		// Some dependencies need input - only publish UserInputRequired events
		// Do NOT dispatch any commands until all inputs are provided
		for _, dep := range deps {
			if dep.Status == domain.DependencyDeploymentStatusAwaitingInput {
				s.bus.Publish(ctx, events.ProjectUserInputRequiredTopic(projectID), events.UserInputRequired{
					DeploymentId:   d.ID,
					DependencyName: dep.DependencyName,
				})
			}
		}
		return
	}

	// All dependencies have config from previous deployments - dispatch commands
	for _, dep := range deps {
		if dep.Status == domain.DependencyDeploymentStatusPending && dep.CommandID != nil {
			s.bus.Publish(ctx, events.ClusterCommandQueuedTopic(env.ClusterID), events.CommandQueued{
				ClusterID:    env.ClusterID,
				CommandID:    *dep.CommandID,
				DeploymentID: d.ID,
			})
		}
	}
}

func (s *deploymentService) GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error) {
	d, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	return d, nil
}

func (s *deploymentService) ProvideUserInput(ctx context.Context, deploymentID uuid.UUID, depName string, input UserInputRequest) error {
	// Validate the dependency exists and is in awaiting_input status
	cfg, err := s.deploymentInfo.GetDepConfig(ctx, deploymentID, depName)
	if err != nil {
		return fmt.Errorf("get dep config: %w", err)
	}
	if cfg.Status != domain.DependencyDeploymentStatusAwaitingInput {
		return fmt.Errorf("dependency %q is not awaiting input (status: %s)", depName, cfg.Status)
	}

	// Update the dependency config with user-provided input
	if err := s.deploymentInfo.UpdateDepConfigUserInput(ctx, deploymentID, depName, input.Managed, input.ProviderInputs, input.UserConfig); err != nil {
		return fmt.Errorf("update dep config: %w", err)
	}

	// Publish UserInputProvided event to trigger scheduling
	s.bus.Publish(ctx, events.TopicUserInputProvided, events.UserInputProvided{
		DeploymentID:   deploymentID,
		DependencyName: depName,
	})

	return nil
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

// Start subscribes to every handler->service topic and drives the deployment
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
		s.bus.Subscribe(events.TopicUserInputProvided, func(v any) {
			e, ok := v.(events.UserInputProvided)
			if !ok {
				log.Printf("deployment service: unexpected event type %T on user_input.provided", v)
				return
			}
			s.handleUserInputProvided(ctx, e)
		}),
	}
	<-ctx.Done()
	for _, u := range unsubs {
		u()
	}
}

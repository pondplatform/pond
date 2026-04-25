package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	resolver       config.ConfigResolver
	tx             Transactor
	bus            events.Bus
	log            *slog.Logger
}

func NewDeploymentService(
	deploymentInfo store.DeploymentInfoStore,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	depSvc DependencyService,
	helmGen helmgen.HelmValuesGenerator,
	tmplRenderer config.TemplateRenderer,
	resolver config.ConfigResolver,
	tx Transactor,
	bus events.Bus,
	log *slog.Logger,
) DeploymentService {
	return &deploymentService{
		deploymentInfo: deploymentInfo,
		services:       services,
		envs:           envs,
		depSvc:         depSvc,
		helmGen:        helmGen,
		tmplRenderer:   tmplRenderer,
		resolver:       resolver,
		tx:             tx,
		bus:            bus,
		log:            log,
	}
}

func (s *deploymentService) Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error) {
	// --- reads (outside the transaction) ---
	// Note: Validate performs the same lookups. Submit re-fetches independently
	// because the handler does not guarantee Validate was called first, and the
	// data may have changed between the two calls.

	env, err := s.envs.GetByName(ctx, req.ProjectID, req.EnvironmentName)
	if err != nil {
		return nil, fmt.Errorf("lookup environment %q: %w", req.EnvironmentName, err)
	}

	// Resolve environment-specific overrides to produce final ServiceConfig
	svcConfig, err := s.resolver.Resolve(&req.OverridableConfig, env.Name)
	if err != nil {
		return nil, fmt.Errorf("resolve config for environment %q: %w", env.Name, err)
	}

	svc, err := s.services.GetByName(ctx, req.ProjectID, svcConfig.Name)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) || !req.CreateIfNotExists {
			return nil, fmt.Errorf("lookup service %q: %w", svcConfig.Name, err)
		}
		svc = &domain.Service{
			ID:        uuid.New(),
			ProjectID: req.ProjectID,
			Name:      svcConfig.Name,
			CreatedAt: time.Now(),
		}
		if err := s.services.Create(ctx, svc); err != nil {
			return nil, fmt.Errorf("create service %q: %w", svcConfig.Name, err)
		}
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
			ServiceConfigSnapshot: *svcConfig,
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
			if _, err := s.enqueueHelm(ctx, tx, d, env); err != nil {
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

	// Apply user input and transition status to pending so AnyDepConfigAwaitingInput
	// no longer counts this row as blocking.
	managed := input.Managed
	cfg.Managed = &managed
	cfg.ProviderInputs = input.ProviderInputs
	cfg.UserConfig = input.UserConfig
	cfg.Status = domain.DependencyDeploymentStatusPending
	if err := s.deploymentInfo.UpdateDepConfig(ctx, cfg); err != nil {
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

	env, err := s.envs.GetByName(ctx, req.ProjectID, req.EnvironmentName)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, domain.ValidationError{
			Component: "environment",
			Field:     "name",
			Message:   fmt.Sprintf("environment %q not found", req.EnvironmentName),
		})
		// Can't continue without environment - needed to resolve config
		return result, nil
	}

	svcConfig, err := s.resolver.Resolve(&req.OverridableConfig, env.Name)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, domain.ValidationError{
			Component: "config",
			Message:   fmt.Sprintf("resolve config: %s", err),
		})
		return result, nil
	}

	_, err = s.services.GetByName(ctx, req.ProjectID, svcConfig.Name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && req.CreateIfNotExists {
			result.Warnings = append(result.Warnings, domain.ValidationWarning{
				Component: "service",
				Message:   fmt.Sprintf("service %q will be created", svcConfig.Name),
			})
		} else {
			result.Valid = false
			result.Errors = append(result.Errors, domain.ValidationError{
				Component: "service",
				Field:     "name",
				Message:   fmt.Sprintf("service %q not found", svcConfig.Name),
			})
		}
	}

	if err := s.depSvc.Validate(ctx, svcConfig.Dependencies); err != nil {
		result.Valid = false
		if ve, ok := err.(*domain.ValidationErrors); ok {
			result.Errors = append(result.Errors, ve.Errors...)
		}
	}

	return result, nil
}

func (s *deploymentService) ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *domain.DeploymentStatus, limit int, cursor string) ([]domain.Deployment, error) {
	return s.deploymentInfo.ListByServiceFiltered(ctx, serviceID, environmentID, status, limit, cursor)
}

func (s *deploymentService) Cancel(ctx context.Context, deploymentID uuid.UUID) error {
	d, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	// Check if deployment is already in a terminal state
	if d.Status == domain.DeploymentStatusSucceeded || d.Status == domain.DeploymentStatusFailed {
		return fmt.Errorf("deployment is already in terminal state: %s: %w", d.Status, domain.ErrInvalidInput)
	}

	// Update status to failed
	now := time.Now()
	if err := s.deploymentInfo.UpdateStatus(ctx, deploymentID, domain.DeploymentStatusFailed, &now); err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}

	// Cancel any queued commands
	if err := s.deploymentInfo.UpdateCommandsByDeployment(ctx, deploymentID, domain.CommandStatusQueued, domain.CommandStatusCancelled); err != nil {
		return fmt.Errorf("cancel queued commands: %w", err)
	}

	return nil
}

// Start subscribes to every handler->service topic and drives the deployment
// state machine in response. Returns an error if any subscription fails.
// Blocks until ctx is cancelled.
func (s *deploymentService) Start(ctx context.Context) error {
	type sub struct {
		topic string
		fn    func() (func(), error)
	}
	subs := []sub{
		{events.TopicCommandResults, func() (func(), error) {
			return s.bus.SubscribeWork(events.TopicCommandResults, func(v any) {
				res, ok := v.(events.CommandResult)
				if !ok {
					s.log.Error("unexpected event type", "topic", events.TopicCommandResults, "type", fmt.Sprintf("%T", v))
					return
				}
				if err := s.processResult(ctx, res); err != nil {
					s.log.Error("processResult", "command_id", res.CommandID, "err", err)
				}
			})
		}},
		{events.TopicAgentReady, func() (func(), error) {
			return s.bus.SubscribeWork(events.TopicAgentReady, func(v any) {
				e, ok := v.(events.AgentReady)
				if !ok {
					s.log.Error("unexpected event type", "topic", events.TopicAgentReady, "type", fmt.Sprintf("%T", v))
					return
				}
				s.handleAgentReady(ctx, e)
			})
		}},
		{events.TopicCommandStarted, func() (func(), error) {
			return s.bus.SubscribeWork(events.TopicCommandStarted, func(v any) {
				e, ok := v.(events.CommandStarted)
				if !ok {
					s.log.Error("unexpected event type", "topic", events.TopicCommandStarted, "type", fmt.Sprintf("%T", v))
					return
				}
				s.handleCommandStarted(ctx, e)
			})
		}},
		{events.TopicCommandLogs, func() (func(), error) {
			return s.bus.SubscribeFanout(events.TopicCommandLogs, func(v any) {
				e, ok := v.(events.CommandLog)
				if !ok {
					s.log.Error("unexpected event type", "topic", events.TopicCommandLogs, "type", fmt.Sprintf("%T", v))
					return
				}
				s.handleCommandLog(ctx, e)
			})
		}},
		{events.TopicAgentDisconnected, func() (func(), error) {
			return s.bus.SubscribeWork(events.TopicAgentDisconnected, func(v any) {
				e, ok := v.(events.AgentDisconnected)
				if !ok {
					s.log.Error("unexpected event type", "topic", events.TopicAgentDisconnected, "type", fmt.Sprintf("%T", v))
					return
				}
				s.handleAgentDisconnected(ctx, e)
			})
		}},
		{events.TopicUserInputProvided, func() (func(), error) {
			return s.bus.SubscribeWork(events.TopicUserInputProvided, func(v any) {
				e, ok := v.(events.UserInputProvided)
				if !ok {
					s.log.Error("unexpected event type", "topic", events.TopicUserInputProvided, "type", fmt.Sprintf("%T", v))
					return
				}
				s.handleUserInputProvided(ctx, e)
			})
		}},
	}

	var unsubs []func()
	for _, s := range subs {
		unsub, err := s.fn()
		if err != nil {
			for _, u := range unsubs {
				u()
			}
			return fmt.Errorf("subscribe %s: %w", s.topic, err)
		}
		unsubs = append(unsubs, unsub)
	}

	<-ctx.Done()
	for _, u := range unsubs {
		u()
	}
	return nil
}

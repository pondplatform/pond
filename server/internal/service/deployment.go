package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/helmgen"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

type deploymentService struct {
	deploymentInfo store.DeploymentInfoStore
	services       store.ServiceRepository
	envs           store.EnvironmentRepository
	depSvc         DependencyService
	helmGen        helmgen.HelmValuesGenerator
	tmplRenderer   TemplateRenderer
	resolver       config.ConfigResolver
	tx             Transactor
	topics         events.Topics
	log            *slog.Logger
}

func NewDeploymentService(
	deploymentInfo store.DeploymentInfoStore,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	depSvc DependencyService,
	helmGen helmgen.HelmValuesGenerator,
	tmplRenderer TemplateRenderer,
	resolver config.ConfigResolver,
	tx Transactor,
	topics events.Topics,
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
		topics:         topics,
		log:            log,
	}
}

func (s *deploymentService) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]domain.CommandLog, error) {
	if _, err := s.deploymentInfo.GetCommand(ctx, commandID); err != nil {
		return nil, err
	}
	return s.deploymentInfo.GetCommandLogs(ctx, commandID)
}

func (s *deploymentService) Submit(ctx context.Context, req api.SubmitRequest) (*domain.Deployment, error) {
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

	// Reject a malformed config here rather than letting it reach the cluster as
	// a failed rollout the user has to read helm output to understand.
	if err := config.Validate(svcConfig); err != nil {
		return nil, err
	}

	svc, err := s.services.GetByName(ctx, req.ProjectID, svcConfig.Name)
	if err != nil {
		if !errors.Is(err, api.ErrNotFound) || !req.CreateIfNotExists {
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
	var status domain.DependencyDeploymentStatus
	err = s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		now := time.Now()
		d = &domain.Deployment{
			ID:                    uuid.New(),
			ServiceID:             svc.ID,
			EnvironmentID:         env.ID,
			ImageTag:              req.ImageTag,
			ServiceConfigSnapshot: *svcConfig,
			Status:                api.DeploymentStatusAwaitingInput,
			TriggeredBy:           req.TriggeredBy,
			CreatedAt:             now,
		}

		if err := tx.DeploymentInfo.Create(ctx, d); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}

		var err error
		status, err = s.depSvc.CreateDependencyDeployments(ctx, tx, svc, d)
		return err
	})
	if err != nil {
		return nil, err
	}

	if err := s.advanceOnDependencyStatus(ctx, d, env, status); err != nil {
		return nil, err
	}

	return d, nil
}

func (s *deploymentService) launchCommand(ctx context.Context, command *domain.Command, deploymentId uuid.UUID) error {
	// Persist before publishing. CommandQueued only wakes the agent, which then
	// asks for the next queued command by querying this table — and a wake that
	// finds nothing is never retried, so publishing first can strand the command
	// in 'queued' forever.
	if err := s.deploymentInfo.CreateCommand(ctx, command); err != nil {
		return err
	}

	if err := s.topics.CommandQueuedPublisher(command.ClusterID).Publish(ctx, events.CommandQueued{
		ClusterID:    command.ClusterID,
		CommandID:    command.ID,
		DeploymentID: deploymentId,
	}); err != nil {
		return fmt.Errorf("notify cluster %s of queued command: %w", command.ClusterID, err)
	}

	return nil
}

func (s *deploymentService) ConfigureDeployment(ctx context.Context, deploymentID uuid.UUID, inputs map[string]api.DependencyInput) error {
	for depName, input := range inputs {
		cfg, err := s.deploymentInfo.GetDepConfig(ctx, deploymentID, depName)
		if err != nil {
			return fmt.Errorf("get dep config %q: %w", depName, err)
		}

		managed := input.Managed
		cfg.Managed = &managed
		if input.Managed {
			cfg.ProviderInputs = input.Values
		} else {
			cfg.UserConfig = input.Values
		}
		cfg.Status = domain.DependencyDeploymentStatusPending

		if err := s.deploymentInfo.UpdateDepConfig(ctx, cfg); err != nil {
			return fmt.Errorf("update dep config %q: %w", depName, err)
		}
	}

	if err := s.topics.UserInputProvidedPublisher().Publish(ctx, events.UserInputProvided{
		DeploymentID: deploymentID,
	}); err != nil {
		return fmt.Errorf("notify user input provided: %w", err)
	}

	return nil
}

func (s *deploymentService) GetStatus(ctx context.Context, deploymentID uuid.UUID) (*DeploymentDetail, error) {
	d, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}

	deps, err := s.deploymentInfo.ListDepConfigs(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list dep configs: %w", err)
	}

	// Collect the unique command IDs referenced by deps plus the helm command.
	commandIDs := make(map[uuid.UUID]struct{})
	for _, dep := range deps {
		if dep.CommandID != nil {
			commandIDs[*dep.CommandID] = struct{}{}
		}
	}
	if d.HelmCommandID != nil {
		commandIDs[*d.HelmCommandID] = struct{}{}
	}

	commands := make([]*domain.Command, 0, len(commandIDs))
	for id := range commandIDs {
		cmd, err := s.deploymentInfo.GetCommand(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get command %s: %w", id, err)
		}
		commands = append(commands, cmd)
	}

	return &DeploymentDetail{
		Deployment:   d,
		Dependencies: deps,
		Commands:     commands,
	}, nil
}

func (s *deploymentService) Validate(ctx context.Context, req api.SubmitRequest) (*ValidationResult, error) {
	result := &ValidationResult{Valid: true}

	env, err := s.envs.GetByName(ctx, req.ProjectID, req.EnvironmentName)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, api.ValidationError{
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
		result.Errors = append(result.Errors, api.ValidationError{
			Component: "config",
			Message:   fmt.Sprintf("resolve config: %s", err),
		})
		return result, nil
	}

	if err := config.Validate(svcConfig); err != nil {
		result.Valid = false
		var ve *api.ValidationErrors
		if errors.As(err, &ve) {
			result.Errors = append(result.Errors, ve.Errors...)
		}
	}

	_, err = s.services.GetByName(ctx, req.ProjectID, svcConfig.Name)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) && req.CreateIfNotExists {
			result.Warnings = append(result.Warnings, api.ValidationWarning{
				Component: "service",
				Message:   fmt.Sprintf("service %q will be created", svcConfig.Name),
			})
		} else {
			result.Valid = false
			result.Errors = append(result.Errors, api.ValidationError{
				Component: "service",
				Field:     "name",
				Message:   fmt.Sprintf("service %q not found", svcConfig.Name),
			})
		}
	}

	if err := s.depSvc.Validate(ctx, svcConfig.Dependencies); err != nil {
		result.Valid = false
		if ve, ok := err.(*api.ValidationErrors); ok {
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
	if d.Status == api.DeploymentStatusSucceeded || d.Status == api.DeploymentStatusFailed {
		return fmt.Errorf("deployment is already in terminal state: %s: %w", d.Status, api.ErrInvalidInput)
	}

	// Update status to failed
	now := time.Now()
	if err := s.deploymentInfo.UpdateStatus(ctx, deploymentID, api.DeploymentStatusCancelled, &now); err != nil {
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
	var unsubs []func()
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	for _, subscribe := range []func() (func(), error){
		func() (func(), error) {
			return s.topics.CommandResultsSubscriber().Subscribe(ctx, s.handleCommandResult)
		},
		func() (func(), error) {
			return s.topics.AgentReadySubscriber().Subscribe(ctx, s.handleAgentReady)
		},
		func() (func(), error) {
			return s.topics.CommandStartedSubscriber().Subscribe(ctx, s.handleCommandStarted)
		},
		func() (func(), error) {
			return s.topics.CommandLogsSubscriber().Subscribe(ctx, s.handleCommandLog)
		},
		func() (func(), error) {
			return s.topics.AgentDisconnectedSubscriber().Subscribe(ctx, s.handleAgentDisconnected)
		},
		func() (func(), error) {
			return s.topics.UserInputProvidedSubscriber().Subscribe(ctx, s.handleUserInputProvided)
		},
	} {
		unsub, err := subscribe()
		if err != nil {
			return err
		}
		unsubs = append(unsubs, unsub)
	}

	<-ctx.Done()
	return nil
}

func (s *deploymentService) storeDeploymentError(ctx context.Context, deploymentId uuid.UUID, err error) error {
	now := time.Now()
	return s.deploymentInfo.SetFailed(ctx, deploymentId, err.Error(), &now)
}

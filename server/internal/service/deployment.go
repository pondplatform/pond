package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

type deploymentService struct {
	deploymentInfo store.DeploymentInfoStore
	services       store.ServiceRepository
	envs           store.EnvironmentRepository
	dependencies   dependencyService
	stateMachine   deploymentStateMachine
	resolver       config.ConfigResolver
	tx             Transactor
}

func NewDeploymentService(
	deploymentInfo store.DeploymentInfoStore,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	specs dependency.SpecRegistry,
	helmGen helmgen.HelmValuesGenerator,
	tmplRenderer TemplateRenderer,
	resolver config.ConfigResolver,
	tx Transactor,
	topics events.Topics,
	log *slog.Logger,
) (DeploymentService, *EventRouter) {
	dependencies := &internalDependencyService{
		specs:          specs,
		envs:           envs,
		deploymentInfo: deploymentInfo,
		tx:             tx,
		topics:         topics,
	}
	machine := &stateMachine{
		deploymentInfo: deploymentInfo,
		envs:           envs,
		dependencies:   dependencies,
		helmGen:        helmGen,
		tmplRenderer:   tmplRenderer,
		tx:             tx,
		topics:         topics,
		log:            log,
	}
	service := &deploymentService{
		deploymentInfo: deploymentInfo,
		services:       services,
		envs:           envs,
		dependencies:   dependencies,
		stateMachine:   machine,
		resolver:       resolver,
		tx:             tx,
	}
	router := &EventRouter{
		deploymentInfo: deploymentInfo,
		stateMachine:   machine,
		topics:         topics,
		log:            log,
	}
	return service, router
}

func (s *deploymentService) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]db.CommandLog, error) {
	if _, err := s.deploymentInfo.GetCommand(ctx, commandID); err != nil {
		return nil, err
	}
	return s.deploymentInfo.GetCommandLogs(ctx, commandID)
}

func (s *deploymentService) Submit(ctx context.Context, req api.SubmitRequest) (*db.Deployment, error) {
	env, err := s.lookupEnvironment(ctx, req.ProjectID, req.EnvironmentName)
	if err != nil {
		return nil, err
	}
	svcConfig, err := s.resolveServiceConfig(req, env.Name)
	if err != nil {
		return nil, err
	}
	svc, err := s.getOrCreateService(ctx, req, svcConfig.Name)
	if err != nil {
		return nil, err
	}
	previous, err := s.previousDependencies(ctx, svc)
	if err != nil {
		return nil, err
	}

	deployment := newDeployment(req, svc.ID, env.ID, *svcConfig)
	dependencies := newDependencyDeployments(deployment, req.Dependencies, previous)
	deployment.Status = initialStatus(dependencies)

	if err := s.createDeployment(ctx, deployment, dependencies); err != nil {
		return nil, err
	}
	if err := s.stateMachine.InitializeDeployment(ctx, deployment, dependencies); err != nil {
		return nil, err
	}
	return &deployment, nil
}

func (s *deploymentService) lookupEnvironment(ctx context.Context, projectID uuid.UUID, name string) (*db.Environment, error) {
	env, err := s.envs.GetByName(ctx, projectID, name)
	if errors.Is(err, api.ErrNotFound) {
		return nil, s.environmentNotFound(ctx, projectID, name)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup environment %q: %w", name, err)
	}
	return env, nil
}

func (s *deploymentService) resolveServiceConfig(req api.SubmitRequest, envName string) (*serviceconfig.ServiceConfig, error) {
	svcConfig, err := s.resolver.Resolve(&req.OverridableConfig, envName)
	if err != nil {
		return nil, fmt.Errorf("resolve config for environment %q: %w", envName, err)
	}
	// Reject a malformed config here rather than letting it reach the cluster as
	// a failed rollout the user has to read helm output to understand.
	if err := config.Validate(svcConfig); err != nil {
		return nil, err
	}
	if req.Dependencies != nil {
		if err := validateDependencyInputs(svcConfig.Dependencies, req.Dependencies); err != nil {
			return nil, err
		}
	}
	return svcConfig, nil
}

func (s *deploymentService) getOrCreateService(ctx context.Context, req api.SubmitRequest, name string) (*db.Service, error) {
	svc, err := s.services.GetByName(ctx, req.ProjectID, name)
	if err == nil {
		return svc, nil
	}
	if !errors.Is(err, api.ErrNotFound) || !req.CreateIfNotExists {
		return nil, fmt.Errorf("lookup service %q: %w", name, err)
	}
	svc = &db.Service{
		ID:        uuid.New(),
		ProjectID: req.ProjectID,
		Name:      name,
		CreatedAt: time.Now(),
	}
	if err := s.services.Create(ctx, svc); err != nil {
		return nil, fmt.Errorf("create service %q: %w", name, err)
	}
	return svc, nil
}

func (s *deploymentService) previousDependencies(ctx context.Context, svc *db.Service) (map[string]db.DependencyDeployment, error) {
	if svc.CurrentDeploymentID == nil {
		return nil, nil
	}
	dependencies, err := s.deploymentInfo.ListDepConfigs(ctx, *svc.CurrentDeploymentID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies of the current deployment: %w", err)
	}
	byName := make(map[string]db.DependencyDeployment, len(dependencies))
	for _, d := range dependencies {
		byName[d.DependencyName] = d
	}
	return byName, nil
}

func newDeployment(req api.SubmitRequest, serviceID, environmentID uuid.UUID, svcConfig serviceconfig.ServiceConfig) db.Deployment {
	return db.Deployment{
		ID:                    uuid.New(),
		ServiceID:             serviceID,
		EnvironmentID:         environmentID,
		ImageTag:              req.ImageTag,
		ServiceConfigSnapshot: svcConfig,
		TriggeredBy:           req.TriggeredBy,
		CreatedAt:             time.Now(),
	}
}

func (s *deploymentService) createDeployment(ctx context.Context, deployment db.Deployment, dependencies []db.DependencyDeployment) error {
	return s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		if err := tx.DeploymentInfo.Create(ctx, &deployment); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}
		for i := range dependencies {
			if err := tx.DeploymentInfo.CreateDepConfig(ctx, &dependencies[i]); err != nil {
				return fmt.Errorf("create dependency %q: %w", dependencies[i].DependencyName, err)
			}
		}
		return nil
	})
}

func validateDependencyInputs(declared map[string]serviceconfig.DependencyDeclaration, inputs map[string]api.DependencyInput) error {
	var errs api.ValidationErrors
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if _, ok := inputs[name]; !ok {
			errs.Add("dependency", name, "missing input")
		}
	}
	addUndeclaredInputErrors(&errs, declared, inputs)
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

func validateDeclaredInputs(declared map[string]serviceconfig.DependencyDeclaration, inputs map[string]api.DependencyInput) error {
	var errs api.ValidationErrors
	addUndeclaredInputErrors(&errs, declared, inputs)
	if errs.HasErrors() {
		return &errs
	}
	return nil
}

func addUndeclaredInputErrors(errs *api.ValidationErrors, declared map[string]serviceconfig.DependencyDeclaration, inputs map[string]api.DependencyInput) {
	for _, name := range slices.Sorted(maps.Keys(inputs)) {
		if _, ok := declared[name]; !ok {
			errs.Add("dependency", name, "not declared in service config")
		}
	}
}

func (s *deploymentService) ConfigureDeployment(ctx context.Context, deploymentID uuid.UUID, inputs map[string]api.DependencyInput) error {
	deployment, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}
	if err := validateDeclaredInputs(deployment.ServiceConfigSnapshot.Dependencies, inputs); err != nil {
		return err
	}
	current, err := s.deploymentInfo.ListDepConfigs(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("list dependencies: %w", err)
	}

	dependencies := applyInputs(current, inputs)
	if err := s.saveConfiguredDependencies(ctx, dependencies, inputs); err != nil {
		return err
	}
	return s.stateMachine.InitializeDeployment(ctx, *deployment, dependencies)
}

// saveConfiguredDependencies writes only the rows named in inputs: rewriting
// the others could overwrite a status a command result has just recorded.
func (s *deploymentService) saveConfiguredDependencies(ctx context.Context, dependencies []db.DependencyDeployment, inputs map[string]api.DependencyInput) error {
	for i := range dependencies {
		if _, ok := inputs[dependencies[i].DependencyName]; !ok {
			continue
		}
		if err := s.deploymentInfo.UpdateDepConfig(ctx, &dependencies[i]); err != nil {
			return fmt.Errorf("update dependency %q: %w", dependencies[i].DependencyName, err)
		}
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

	commands := make([]*db.Command, 0, len(commandIDs))
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

	if err := s.dependencies.Validate(ctx, svcConfig.Dependencies); err != nil {
		result.Valid = false
		if ve, ok := err.(*api.ValidationErrors); ok {
			result.Errors = append(result.Errors, ve.Errors...)
		}
	}

	return result, nil
}

func (s *deploymentService) ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *db.DeploymentStatus, limit int, cursor string) ([]db.Deployment, error) {
	return s.deploymentInfo.ListByServiceFiltered(ctx, serviceID, environmentID, status, limit, cursor)
}

func (s *deploymentService) Cancel(ctx context.Context, deploymentID uuid.UUID) error {
	d, err := s.deploymentInfo.GetByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	if d.Status == api.DeploymentStatusSucceeded || d.Status == api.DeploymentStatusFailed {
		return api.Conflict("deployment %s has already %s and can no longer be cancelled", deploymentID, d.Status)
	}

	now := time.Now()
	if err := s.deploymentInfo.UpdateStatus(ctx, deploymentID, api.DeploymentStatusCancelled, &now); err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}

	if err := s.deploymentInfo.UpdateCommandsByDeployment(ctx, deploymentID, db.CommandStatusQueued, db.CommandStatusCancelled); err != nil {
		return fmt.Errorf("cancel queued commands: %w", err)
	}

	return nil
}

// environmentNotFound lists the project's environments, since a typo in the
// environment name is the usual cause. Listing is best effort: the not-found
// error stands on its own.
func (s *deploymentService) environmentNotFound(ctx context.Context, projectID uuid.UUID, name string) error {
	envs, err := s.envs.ListByProject(ctx, projectID)
	if err != nil || len(envs) == 0 {
		return api.NotFound("environment %q not found in project %s", name, projectID)
	}
	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	slices.Sort(names)
	return api.NotFound("environment %q not found in project %s (available: %s)", name, projectID, strings.Join(names, ", "))
}

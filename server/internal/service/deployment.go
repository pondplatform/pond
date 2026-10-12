package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
)

// deploymentService handles requests. A submit plans the whole deployment up
// front; from then on only command results move it, through AgentCommands.
type deploymentService struct {
	repos    store.Repos
	tx       store.Transactor
	specs    dependency.SpecRegistry
	resolver config.ConfigResolver
	helmGen  helmgen.HelmValuesGenerator
	log      *slog.Logger
	now      func() time.Time
}

// NewDeploymentService wires the deployment service and the agent-facing
// side that hands out its commands.
func NewDeploymentService(
	repos store.Repos,
	tx store.Transactor,
	specs dependency.SpecRegistry,
	resolver config.ConfigResolver,
	log *slog.Logger,
) (DeploymentService, *AgentCommands) {
	helmGen := helmgen.NewGenerator()
	service := &deploymentService{
		repos:    repos,
		tx:       tx,
		specs:    specs,
		resolver: resolver,
		helmGen:  helmGen,
		log:      log,
		now:      time.Now,
	}
	agents := &AgentCommands{
		repos:   repos,
		tx:      tx,
		helmGen: helmGen,
		log:     log.With("component", "agent_commands"),
		now:     time.Now,
	}
	return service, agents
}

func (s *deploymentService) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]db.CommandLog, error) {
	if _, err := s.repos.Deployments.GetCommand(ctx, commandID); err != nil {
		return nil, err
	}
	return s.repos.Deployments.GetCommandLogs(ctx, commandID)
}

// Submit verifies the request, resolves every dependency's input and plans
// the deployment's commands, all in one transaction: either the whole
// deployment is created, or nothing is.
func (s *deploymentService) Submit(ctx context.Context, req api.SubmitRequest) (*db.Deployment, error) {
	env, svcConfig, err := s.validateSubmission(ctx, req)
	if err != nil {
		return nil, err
	}

	var deployment db.Deployment
	err = s.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		svc, err := getOrCreateService(ctx, tx.Services, req, svcConfig.Name, s.now())
		if err != nil {
			return err
		}
		previous, err := previousDependencies(ctx, tx.Deployments, svc)
		if err != nil {
			return err
		}
		inputs, err := resolveInputs(svcConfig.Dependencies, req.Dependencies, previous)
		if err != nil {
			return err
		}

		deployment = newDeployment(req, svc.ID, env.ID, *svcConfig, s.now())
		dependencies, commands, err := plan(deployment, *env, inputs)
		if err != nil {
			return err
		}
		if _, err := activate(deployment, *env, dependencies, commands, firstPhase(commands), s.helmGen); err != nil {
			// Nothing has run, so a config that cannot render is the request's
			// fault.
			var errs api.ValidationErrors
			errs.Add("config", "configs", err.Error())
			return &errs
		}
		for _, cmd := range commands {
			if cmd.Type == db.CommandTypeHelmUpgrade {
				deployment.HelmCommandID = &cmd.ID
			}
		}

		if err := tx.Deployments.Create(ctx, &deployment); err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}
		for i := range dependencies {
			if err := tx.Deployments.CreateDepConfig(ctx, &dependencies[i]); err != nil {
				return fmt.Errorf("create dependency %q: %w", dependencies[i].DependencyName, err)
			}
		}
		for _, cmd := range commands {
			if err := tx.Deployments.CreateCommand(ctx, cmd); err != nil {
				return fmt.Errorf("create %s command: %w", cmd.Type, err)
			}
		}
		return tx.Services.SetCurrentDeployment(ctx, svc.ID, deployment.ID)
	})
	if err != nil {
		return nil, err
	}
	return &deployment, nil
}

func getOrCreateService(ctx context.Context, services store.ServiceRepository, req api.SubmitRequest, name string, now time.Time) (*db.Service, error) {
	svc, err := services.GetByName(ctx, req.ProjectID, name)
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
		CreatedAt: now,
	}
	if err := services.Create(ctx, svc); err != nil {
		return nil, fmt.Errorf("create service %q: %w", name, err)
	}
	return svc, nil
}

// previousDependencies returns the dependencies of the service's most recent
// deployment, by name, so their input can be reused.
func previousDependencies(ctx context.Context, deployments store.DeploymentInfoStore, svc *db.Service) (map[string]db.DependencyDeployment, error) {
	if svc.CurrentDeploymentID == nil {
		return nil, nil
	}
	dependencies, err := deployments.ListDepConfigs(ctx, *svc.CurrentDeploymentID)
	if err != nil {
		return nil, fmt.Errorf("list dependencies of the current deployment: %w", err)
	}
	byName := make(map[string]db.DependencyDeployment, len(dependencies))
	for _, d := range dependencies {
		byName[d.DependencyName] = d
	}
	return byName, nil
}

func newDeployment(req api.SubmitRequest, serviceID, environmentID uuid.UUID, svcConfig serviceconfig.ServiceConfig, now time.Time) db.Deployment {
	return db.Deployment{
		ID:                    uuid.New(),
		ServiceID:             serviceID,
		EnvironmentID:         environmentID,
		ImageTag:              req.ImageTag,
		ServiceConfigSnapshot: svcConfig,
		Status:                db.DeploymentInProgress,
		TriggeredBy:           req.TriggeredBy,
		CreatedAt:             now,
	}
}

func (s *deploymentService) GetStatus(ctx context.Context, deploymentID uuid.UUID) (*DeploymentDetail, error) {
	d, err := s.repos.Deployments.GetByID(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("get deployment: %w", err)
	}
	deps, err := s.repos.Deployments.ListDepConfigs(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list dep configs: %w", err)
	}
	commands, err := s.repos.Deployments.ListCommandsByDeployment(ctx, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return &DeploymentDetail{
		Deployment:   d,
		Dependencies: deps,
		Commands:     commands,
	}, nil
}

func (s *deploymentService) ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *api.DeploymentStatus, limit int, cursor string) ([]db.Deployment, error) {
	return s.repos.Deployments.ListByServiceFiltered(ctx, serviceID, environmentID, status, limit, cursor)
}

// Cancel ends the deployment and cancels its commands, including one an agent
// is running: its late result is then recorded but changes nothing.
func (s *deploymentService) Cancel(ctx context.Context, deploymentID uuid.UUID) error {
	return s.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		d, err := tx.Deployments.LockDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		if d.Status != db.DeploymentInProgress {
			return api.Conflict("deployment %s has already %s and can no longer be cancelled", deploymentID, d.APIStatus())
		}
		return finish(ctx, tx, d, db.DeploymentCancelled, "", s.now())
	})
}

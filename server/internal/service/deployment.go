package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
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
	machine  *stateMachine
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
	machine := &stateMachine{helmGen: helmgen.NewGenerator(), now: time.Now}
	service := &deploymentService{
		repos:    repos,
		tx:       tx,
		specs:    specs,
		resolver: resolver,
		machine:  machine,
		log:      log,
		now:      time.Now,
	}
	agents := &AgentCommands{
		repos:   repos,
		tx:      tx,
		machine: machine,
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

// Submit verifies the request, resolves every dependency's input, plans the
// deployment and enters the state machine, all in one transaction: either the
// deployment is created and started, or nothing is.
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
		if err := verifyVolumesDoNotShrink(ctx, tx.Deployments, svc.ID, env.ID, svcConfig.Volumes); err != nil {
			return err
		}
		inputs, err := resolveInputs(svcConfig.Dependencies, req.Dependencies)
		if err != nil {
			return err
		}
		if err := verifyUnmanagedReferences(svcConfig, inputs); err != nil {
			return err
		}

		deployment = newDeployment(req, svc.ID, env.ID, *svcConfig, s.now())
		dependencies, commands, err := plan(deployment, *env, inputs)
		if err != nil {
			return err
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
		if err := tx.Services.SetCurrentDeployment(ctx, svc.ID, deployment.ID); err != nil {
			return err
		}
		return s.machine.advance(ctx, tx, &deployment)
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

// verifyVolumesDoNotShrink rejects a volume smaller than the environment's last
// succeeded deployment gave it: Kubernetes can grow a claim but never shrink
// it. A failed deployment doesn't count, so a failed grow can be reverted.
func verifyVolumesDoNotShrink(ctx context.Context, deployments store.DeploymentInfoStore, serviceID, environmentID uuid.UUID, volumes map[string]serviceconfig.VolumeSpec) error {
	if len(volumes) == 0 {
		return nil
	}
	succeeded := api.DeploymentStatusSucceeded
	last, err := deployments.ListByServiceFiltered(ctx, serviceID, &environmentID, &succeeded, 1, "")
	if err != nil {
		return fmt.Errorf("get the last succeeded deployment: %w", err)
	}
	if len(last) == 0 {
		return nil
	}

	previous := last[0].ServiceConfigSnapshot.Volumes
	var errs api.ValidationErrors
	for _, name := range slices.Sorted(maps.Keys(volumes)) {
		before, ok := previous[name]
		if !ok {
			continue
		}
		// Both sizes passed validation when they were submitted.
		was, _ := config.VolumeSizeBytes(before.Size)
		now, _ := config.VolumeSizeBytes(volumes[name].Size)
		if now < was {
			errs.Add("config", fmt.Sprintf("volumes[%q].size", name), fmt.Sprintf(
				"must be at least %s, its size in the last successful deployment: a volume can grow but never shrink, got %s",
				before.Size, volumes[name].Size))
		}
	}
	if errs.HasErrors() {
		return &errs
	}
	return nil
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

// Cancel stops the deployment: commands that have not started are cancelled
// at once, and an agent running one is told to cancel it.
func (s *deploymentService) Cancel(ctx context.Context, deploymentID uuid.UUID) error {
	return s.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		d, err := tx.Deployments.LockDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		return s.machine.cancel(ctx, tx, d)
	})
}

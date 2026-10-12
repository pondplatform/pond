package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// deploymentService handles requests: it validates them, records what they
// change in one transaction, and leaves deciding what happens next to the
// orchestrator.
type deploymentService struct {
	repos        store.Repos
	tx           store.Transactor
	specs        dependency.SpecRegistry
	resolver     config.ConfigResolver
	orchestrator *orchestrator
	log          *slog.Logger
	now          func() time.Time
}

func NewDeploymentService(
	repos store.Repos,
	tx store.Transactor,
	specs dependency.SpecRegistry,
	resolver config.ConfigResolver,
	topics events.Topics,
	log *slog.Logger,
) (DeploymentService, *EventRouter) {
	machine := &orchestrator{
		repos:   repos,
		tx:      tx,
		helmGen: helmgen.NewGenerator(),
		topics:  topics,
		log:     log,
		now:     time.Now,
	}
	service := &deploymentService{
		repos:        repos,
		tx:           tx,
		specs:        specs,
		resolver:     resolver,
		orchestrator: machine,
		log:          log,
		now:          time.Now,
	}
	router := &EventRouter{
		deploymentInfo: repos.Deployments,
		results:        machine,
		topics:         topics,
		log:            log,
	}
	return service, router
}

func (s *deploymentService) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]db.CommandLog, error) {
	if _, err := s.repos.Deployments.GetCommand(ctx, commandID); err != nil {
		return nil, err
	}
	return s.repos.Deployments.GetCommandLogs(ctx, commandID)
}

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
		deployment = newDeployment(req, svc.ID, env.ID, *svcConfig, s.now())
		dependencies := newDependencyDeployments(deployment, req.Dependencies, previous)
		deployment.Status = initialStatus(dependencies)
		if err := createDeployment(ctx, tx.Deployments, deployment, dependencies); err != nil {
			return err
		}
		return tx.Services.SetCurrentDeployment(ctx, svc.ID, deployment.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.reconcileAfterRequest(ctx, deployment), nil
}

// reconcileAfterRequest reconciles a deployment the request has already
// committed. A failure does not fail the request: the deployment exists, and
// a later reconcile picks it up.
func (s *deploymentService) reconcileAfterRequest(ctx context.Context, deployment db.Deployment) *db.Deployment {
	reconciled, err := s.orchestrator.Reconcile(ctx, deployment.ID)
	if err != nil {
		s.log.Error("reconcile after request", "deployment_id", deployment.ID, "err", err)
		return &deployment
	}
	return reconciled
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
		TriggeredBy:           req.TriggeredBy,
		CreatedAt:             now,
	}
}

func createDeployment(ctx context.Context, deployments store.DeploymentInfoStore, deployment db.Deployment, dependencies []db.DependencyDeployment) error {
	if err := deployments.Create(ctx, &deployment); err != nil {
		return fmt.Errorf("create deployment: %w", err)
	}
	for i := range dependencies {
		if err := deployments.CreateDepConfig(ctx, &dependencies[i]); err != nil {
			return fmt.Errorf("create dependency %q: %w", dependencies[i].DependencyName, err)
		}
	}
	return nil
}

func (s *deploymentService) ConfigureDeployment(ctx context.Context, deploymentID uuid.UUID, inputs map[string]api.DependencyInput) error {
	var deployment *db.Deployment
	err := s.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		var err error
		deployment, err = tx.Deployments.LockDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		if isTerminal(deployment.Status) {
			return api.Conflict("deployment %s is already %s; submit a new deployment to change its dependencies", deploymentID, deployment.Status)
		}
		if deployment.HelmCommandID != nil {
			return api.Conflict("deployment %s is already rolling out; submit a new deployment to change its dependencies", deploymentID)
		}
		if err := validateDeclaredInputs(deployment.ServiceConfigSnapshot.Dependencies, inputs); err != nil {
			return err
		}
		current, err := tx.Deployments.ListDepConfigs(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("list dependencies: %w", err)
		}
		for _, configured := range applyInputs(current, inputs) {
			if err := tx.Deployments.UpdateDepConfig(ctx, &configured); err != nil {
				return fmt.Errorf("update dependency %q: %w", configured.DependencyName, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.reconcileAfterRequest(ctx, *deployment)
	return nil
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

func (s *deploymentService) ListByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *db.DeploymentStatus, limit int, cursor string) ([]db.Deployment, error) {
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
		if isTerminal(d.Status) {
			return api.Conflict("deployment %s is already %s and can no longer be cancelled", deploymentID, d.Status)
		}
		now := s.now()
		d.Status = api.DeploymentStatusCancelled
		d.CompletedAt = &now
		if err := tx.Deployments.SaveDeployment(ctx, d); err != nil {
			return err
		}
		if err := tx.Deployments.UpdateCommandsByDeployment(ctx, deploymentID,
			db.CommandStatusCancelled, db.CommandStatusQueued, db.CommandStatusDispatched); err != nil {
			return fmt.Errorf("cancel commands: %w", err)
		}
		return nil
	})
}

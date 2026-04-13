package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/helmgen"
	"github.com/pondplatform/pond/internal/server/store"
)

type deploymentService struct {
	deployments store.DeploymentRepository
	services    store.ServiceRepository
	envs        store.EnvironmentRepository
	depConfigs  store.DependencyConfigRepository
	depRequests DependencyDeploymentRequestRepository
	resolver    dependency.DependencyResolver
	helmGen     helmgen.HelmValuesGenerator
	queue       CommandQueue
}

func NewDeploymentService(
	deployments store.DeploymentRepository,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	depConfigs store.DependencyConfigRepository,
	depRequests DependencyDeploymentRequestRepository,
	resolver dependency.DependencyResolver,
	helmGen helmgen.HelmValuesGenerator,
	queue CommandQueue,
) DeploymentService {
	return &deploymentService{
		deployments: deployments,
		services:    services,
		envs:        envs,
		depConfigs:  depConfigs,
		depRequests: depRequests,
		resolver:    resolver,
		helmGen:     helmGen,
		queue:       queue,
	}
}

func (s *deploymentService) Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error) {
	svc, err := s.services.GetByName(ctx, req.ProjectID, req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("lookup service %q: %w", req.ServiceConfig.Name, err)
	}

	env, err := s.envs.GetByID(ctx, req.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("lookup environment: %w", err)
	}

	// Create deployment record.
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
	if err := s.deployments.Create(ctx, d); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}

	// Enqueue tofu.apply for each managed dependency.
	var managedCount int
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

		cmd := &Command{
			ID:           uuid.New(),
			DeploymentID: d.ID,
			Type:         "tofu.apply",
			Payload:      payload,
			CreatedAt:    time.Now(),
		}
		if err := s.queue.Enqueue(ctx, env.ClusterID, cmd); err != nil {
			return nil, fmt.Errorf("enqueue tofu command for %q: %w", depName, err)
		}
		if err := s.depRequests.Create(ctx, &domain.DependencyDeploymentRequest{
			ID:             uuid.New(),
			DeploymentID:   d.ID,
			CommandID:      cmd.ID,
			DependencyName: depName,
			Status:         domain.DependencyRequestStatusPending,
		}); err != nil {
			return nil, fmt.Errorf("create dependency request for %q: %w", depName, err)
		}
		managedCount++
	}

	// No managed dependencies: go straight to helm.
	if managedCount == 0 {
		contexts, err := s.resolver.ResolveAll(ctx, svc.ID, env.ID, req.ServiceConfig.Dependencies)
		if err != nil {
			return nil, fmt.Errorf("resolve dependencies: %w", err)
		}
		if err := s.enqueueHelm(ctx, d, env, contexts); err != nil {
			return nil, err
		}
	}

	return d, nil
}

func (s *deploymentService) enqueueHelm(ctx context.Context, d *domain.Deployment, env *domain.Environment, contexts map[string]domain.ResolvedContext) error {
	helmVals, err := s.helmGen.Generate(&d.ServiceConfigSnapshot, env, contexts)
	if err != nil {
		return fmt.Errorf("generate helm values: %w", err)
	}
	payload, err := json.Marshal(helmVals)
	if err != nil {
		return fmt.Errorf("marshal helm values: %w", err)
	}
	cmd := &Command{
		ID:           uuid.New(),
		DeploymentID: d.ID,
		Type:         "helm.upgrade",
		Payload:      payload,
		CreatedAt:    time.Now(),
	}
	if err := s.queue.Enqueue(ctx, env.ClusterID, cmd); err != nil {
		return fmt.Errorf("enqueue helm command: %w", err)
	}
	if err := s.deployments.SetHelmCommandID(ctx, d.ID, cmd.ID); err != nil {
		return fmt.Errorf("set helm command id: %w", err)
	}
	return nil
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

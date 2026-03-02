package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/dependency"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/common/helmgen"
)

// Command types used by the deployment service to enqueue agent commands.
type Command struct {
	ID           uuid.UUID
	DeploymentID uuid.UUID
	Type         string
	Payload      json.RawMessage
	CreatedAt    time.Time
}

type CommandResult struct {
	CommandID uuid.UUID
	Success   bool
	Output    json.RawMessage
	Error     string
}

// CommandQueue manages the outbound command queue from server to agents.
type CommandQueue interface {
	Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *Command) error
	Dequeue(ctx context.Context, clusterID uuid.UUID) (*Command, error)
	Acknowledge(ctx context.Context, cmdID uuid.UUID, result *CommandResult) error
}

type deploymentService struct {
	deployments domain.DeploymentRepository
	services    domain.ServiceRepository
	envs        domain.EnvironmentRepository
	resolver    dependency.DependencyResolver
	helmGen     helmgen.HelmValuesGenerator
	queue       CommandQueue
}

func NewDeploymentService(
	deployments domain.DeploymentRepository,
	services domain.ServiceRepository,
	envs domain.EnvironmentRepository,
	resolver dependency.DependencyResolver,
	helmGen helmgen.HelmValuesGenerator,
	queue CommandQueue,
) DeploymentService {
	return &deploymentService{
		deployments: deployments,
		services:    services,
		envs:        envs,
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

	// Resolve dependencies.
	contexts, err := s.resolver.ResolveAll(ctx, svc.ID, env.ID, req.ServiceConfig.Dependencies)
	if err != nil {
		return nil, fmt.Errorf("resolve dependencies: %w", err)
	}

	// Generate Helm values.
	helmVals, err := s.helmGen.Generate(&req.ServiceConfig, env, contexts)
	if err != nil {
		return nil, fmt.Errorf("generate helm values: %w", err)
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

	// Enqueue helm upgrade command for the agent.
	payload, err := json.Marshal(helmVals)
	if err != nil {
		return nil, fmt.Errorf("marshal helm values: %w", err)
	}

	cmd := &Command{
		ID:           uuid.New(),
		DeploymentID: d.ID,
		Type:         "helm.upgrade",
		Payload:      payload,
		CreatedAt:    time.Now(),
	}

	if err := s.queue.Enqueue(ctx, env.ClusterID, cmd); err != nil {
		return nil, fmt.Errorf("enqueue command: %w", err)
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

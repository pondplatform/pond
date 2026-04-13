package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type DeploymentService interface {
	Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error)
	Validate(ctx context.Context, req SubmitRequest) (*ValidationResult, error)
}

// DeploymentAdvancer is called after each command result to advance the
// deployment workflow: enqueue the next step or mark the deployment done.
type DeploymentAdvancer interface {
	Advance(ctx context.Context, result *CommandResult) error
	// MarkDispatched transitions a deployment from pending → running when its
	// first command is sent to the agent.
	MarkDispatched(ctx context.Context, deploymentID uuid.UUID) error
}

type SubmitRequest struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	ServiceConfig domain.ServiceConfig
	ImageTag      string
	TriggeredBy   string
}

type ValidationResult struct {
	Valid    bool
	Errors   []domain.ValidationError
	Warnings []domain.ValidationWarning
}

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
	// Dequeue claims (but does not delete) the next available command for the
	// cluster. The row is deleted only on Acknowledge, giving at-least-once
	// delivery semantics.
	Dequeue(ctx context.Context, clusterID uuid.UUID) (*Command, error)
	// Acknowledge stores the result and hard-deletes the claimed queue row.
	Acknowledge(ctx context.Context, cmdID uuid.UUID, result *CommandResult) error
	// CancelDeployment hard-deletes all unclaimed queue rows for a deployment
	// so that sibling commands do not run after a dependency failure.
	CancelDeployment(ctx context.Context, deploymentID uuid.UUID) error
	// RequeueStaleClaims resets claimed_at for rows that have been claimed
	// longer than maxAge, enabling redelivery after an agent crash.
	RequeueStaleClaims(ctx context.Context, maxAge time.Duration) error
}

// DependencyDeploymentRequestRepository is an alias to the store interface.
type DependencyDeploymentRequestRepository = store.DependencyDeploymentRequestRepository

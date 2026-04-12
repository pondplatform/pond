package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type DeploymentService interface {
	Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error)
	GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error)
	Validate(ctx context.Context, req SubmitRequest) (*ValidationResult, error)
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
	Dequeue(ctx context.Context, clusterID uuid.UUID) (*Command, error)
	Acknowledge(ctx context.Context, cmdID uuid.UUID, result *CommandResult) error
}

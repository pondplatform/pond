package client

import (
	"context"

	"github.com/google/uuid"
	iclient "github.com/pondplatform/pond/cli/internal/client"
	api "github.com/pondplatform/pond/shared/server/api"
)

// ServerClient is the public interface for interacting with the Pond server HTTP API.
type ServerClient interface {
	SubmitDeployment(ctx context.Context, req api.SubmitRequest) (*api.Deployment, error)
	GetDeployment(ctx context.Context, id uuid.UUID) (*api.Deployment, error)
	// CancelDeployment stops an in-progress deployment. The server refuses
	// with a 409 when it has already ended.
	CancelDeployment(ctx context.Context, id uuid.UUID) error
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]api.CommandLog, error)
	ListServices(ctx context.Context, projectID uuid.UUID) ([]api.Service, error)
	ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]api.Environment, error)
	ListDeployments(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, limit int) ([]api.DeploymentListItem, error)
	// DeleteService starts deleting the service from every environment it is
	// live in, and returns the delete deployments; none when it is live nowhere.
	DeleteService(ctx context.Context, serviceID uuid.UUID, req api.DeleteServiceRequest) ([]api.Deployment, error)
}

// NewHTTPClient creates a ServerClient that talks to baseURL without authentication.
func NewHTTPClient(baseURL string) ServerClient {
	return iclient.NewHTTPClient(baseURL)
}

// NewHTTPClientWithToken creates a ServerClient that carries a bearer token on every request.
func NewHTTPClientWithToken(baseURL, token string) ServerClient {
	return iclient.NewHTTPClientWithToken(baseURL, token)
}

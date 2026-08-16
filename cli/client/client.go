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
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]api.CommandLog, error)
	ConfigureDeployment(ctx context.Context, id uuid.UUID, req api.ConfigureDeploymentRequest) error
}

// NewHTTPClient creates a ServerClient that talks to baseURL without authentication.
func NewHTTPClient(baseURL string) ServerClient {
	return iclient.NewHTTPClient(baseURL)
}

// NewHTTPClientWithToken creates a ServerClient that carries a bearer token on every request.
func NewHTTPClientWithToken(baseURL, token string) ServerClient {
	return iclient.NewHTTPClientWithToken(baseURL, token)
}

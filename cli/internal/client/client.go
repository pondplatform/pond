package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	api "github.com/pondplatform/pond/shared/server/api"
)

// ServerClient is the CLI's interface to the Pond server API.
type ServerClient interface {
	SubmitDeployment(ctx context.Context, req api.SubmitRequest) (*api.Deployment, error)
	GetDeployment(ctx context.Context, id uuid.UUID) (*api.Deployment, error)
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]api.CommandLog, error)
	ListServices(ctx context.Context, projectID uuid.UUID) ([]api.Service, error)
	ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]api.Environment, error)
	// ListDeployments returns deployments for a service, newest first. A nil
	// environmentID lists deployments across all environments.
	ListDeployments(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, limit int) ([]api.DeploymentListItem, error)
	// DeleteService starts deleting the service from every environment it is
	// live in, and returns the delete deployments; none when it is live nowhere.
	DeleteService(ctx context.Context, serviceID uuid.UUID, req api.DeleteServiceRequest) ([]api.Deployment, error)
}

type httpClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewHTTPClient(baseURL string) ServerClient {
	return &httpClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

func NewHTTPClientWithToken(baseURL, token string) ServerClient {
	return &httpClient{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{},
	}
}

func (c *httpClient) SubmitDeployment(ctx context.Context, req api.SubmitRequest) (*api.Deployment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/api/v1/deployments", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, readError(resp)
	}

	var d api.Deployment
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &d, nil
}

func (c *httpClient) GetDeployment(ctx context.Context, id uuid.UUID) (*api.Deployment, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/deployments/%s", id), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var d api.Deployment
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &d, nil
}

func (c *httpClient) GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]api.CommandLog, error) {
	return getList[api.CommandLog](ctx, c, fmt.Sprintf("/api/v1/commands/%s/logs", commandID))
}

func (c *httpClient) ListServices(ctx context.Context, projectID uuid.UUID) ([]api.Service, error) {
	return getList[api.Service](ctx, c, fmt.Sprintf("/api/v1/projects/%s/services", projectID))
}

func (c *httpClient) ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]api.Environment, error) {
	return getList[api.Environment](ctx, c, fmt.Sprintf("/api/v1/projects/%s/environments", projectID))
}

func (c *httpClient) ListDeployments(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, limit int) ([]api.DeploymentListItem, error) {
	q := url.Values{}
	if environmentID != nil {
		q.Set("environment_id", environmentID.String())
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	path := fmt.Sprintf("/api/v1/services/%s/deployments", serviceID)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return getList[api.DeploymentListItem](ctx, c, path)
}

func (c *httpClient) DeleteService(ctx context.Context, serviceID uuid.UUID, req api.DeleteServiceRequest) ([]api.Deployment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/services/%s/delete", serviceID), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return nil, readError(resp)
	}

	var result api.DeleteServiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Deployments, nil
}

func (c *httpClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	return resp, nil
}

// getList performs a GET against a paginated list endpoint and returns its items.
func getList[T any](ctx context.Context, c *httpClient, path string) ([]T, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result struct {
		Items []T `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Items, nil
}

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("server error (status %d): %s", resp.StatusCode, string(body))
}

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	api "github.com/pondplatform/pond/shared/server/api"
)

// ServerClient is the CLI's interface to the Pond server API.
type ServerClient interface {
	SubmitDeployment(ctx context.Context, req api.SubmitRequest) (*api.Deployment, error)
	GetDeployment(ctx context.Context, id uuid.UUID) (*api.Deployment, error)
	GetCommandLogs(ctx context.Context, commandID uuid.UUID) ([]api.CommandLog, error)
	ConfigureDeployment(ctx context.Context, id uuid.UUID, req api.ConfigureDeploymentRequest) error
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
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/commands/%s/logs", commandID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var result struct {
		Items []api.CommandLog `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Items, nil
}

func (c *httpClient) ConfigureDeployment(ctx context.Context, id uuid.UUID, req api.ConfigureDeploymentRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/deployments/%s/user-input", id), bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return readError(resp)
	}
	return nil
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

func readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("server error (status %d): %s", resp.StatusCode, string(body))
}

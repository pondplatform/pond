package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/service"
)

// ServerClient is the CLI's interface to the Pond server API.
type ServerClient interface {
	SubmitDeployment(ctx context.Context, req service.SubmitRequest) (*domain.Deployment, error)
	GetDeployment(ctx context.Context, id uuid.UUID) (*domain.Deployment, error)
	SetDependencyConfig(ctx context.Context, cfg *domain.DependencyConfig) error
	GetDependencyConfig(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.DependencyConfig, error)
	ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error)
	ListServices(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error)
}

type httpClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewHTTPClient(baseURL string) ServerClient {
	return &httpClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

func (c *httpClient) SubmitDeployment(ctx context.Context, req service.SubmitRequest) (*domain.Deployment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/deployments", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, readError(resp)
	}

	var d domain.Deployment
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &d, nil
}

func (c *httpClient) GetDeployment(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/deployments/%s", id), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var d domain.Deployment
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &d, nil
}

func (c *httpClient) SetDependencyConfig(ctx context.Context, cfg *domain.DependencyConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	path := fmt.Sprintf("/services/%s/environments/%s/dependencies/%s", cfg.ServiceID, cfg.EnvironmentID, cfg.DependencyName)
	resp, err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
}

func (c *httpClient) GetDependencyConfig(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.DependencyConfig, error) {
	path := fmt.Sprintf("/services/%s/environments/%s/dependencies/%s", serviceID, envID, depName)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var cfg domain.DependencyConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &cfg, nil
}

func (c *httpClient) ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]domain.Environment, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/environments", projectID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var envs []domain.Environment
	if err := json.NewDecoder(resp.Body).Decode(&envs); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return envs, nil
}

func (c *httpClient) ListServices(ctx context.Context, projectID uuid.UUID) ([]domain.Service, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/services", projectID), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var svcs []domain.Service
	if err := json.NewDecoder(resp.Body).Decode(&svcs); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return svcs, nil
}

func (c *httpClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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

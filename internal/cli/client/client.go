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

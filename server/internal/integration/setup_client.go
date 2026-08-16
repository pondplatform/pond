//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	shared "github.com/pondplatform/pond/shared/server/api"
)

type scenarioClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newScenarioClient(baseURL, token string) *scenarioClient {
	return &scenarioClient{baseURL: baseURL, token: token, http: &http.Client{}}
}

func (c *scenarioClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	return resp, nil
}

func readBodyError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("server error (status %d): %s", resp.StatusCode, string(body))
}

type clusterSetupResult struct {
	ID         uuid.UUID `json:"id"`
	AgentToken string    `json:"agentToken"`
}

type projectSetupResult struct {
	ID uuid.UUID `json:"id"`
}

type envSetupResult struct {
	ID uuid.UUID `json:"id"`
}

func (c *scenarioClient) createCluster(ctx context.Context, name string) (clusterSetupResult, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/clusters", map[string]any{"name": name})
	if err != nil {
		return clusterSetupResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return clusterSetupResult{}, readBodyError(resp)
	}
	var result clusterSetupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return clusterSetupResult{}, fmt.Errorf("decode cluster: %w", err)
	}
	return result, nil
}

func (c *scenarioClient) createProject(ctx context.Context, name string) (projectSetupResult, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/projects", map[string]any{"name": name})
	if err != nil {
		return projectSetupResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return projectSetupResult{}, readBodyError(resp)
	}
	var result projectSetupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return projectSetupResult{}, fmt.Errorf("decode project: %w", err)
	}
	return result, nil
}

func (c *scenarioClient) createEnvironment(ctx context.Context, projectID uuid.UUID, name, namespace string, clusterID uuid.UUID) (envSetupResult, error) {
	body := map[string]any{
		"name":      name,
		"namespace": namespace,
		"clusterId": clusterID,
	}
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/environments", projectID), body)
	if err != nil {
		return envSetupResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return envSetupResult{}, readBodyError(resp)
	}
	var result envSetupResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return envSetupResult{}, fmt.Errorf("decode environment: %w", err)
	}
	return result, nil
}

func (c *scenarioClient) configureDeployment(ctx context.Context, deploymentID uuid.UUID, deps map[string]shared.DependencyInput) error {
	body := shared.ConfigureDeploymentRequest{Dependencies: deps}
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/deployments/%s/user-input", deploymentID), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return readBodyError(resp)
	}
	return nil
}

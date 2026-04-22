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
	AgentToken string    `json:"agent_token"`
}

type projectSetupResult struct {
	ID uuid.UUID `json:"id"`
}

type envSetupResult struct {
	ID uuid.UUID `json:"id"`
}

func (c *scenarioClient) createCluster(ctx context.Context, orgID uuid.UUID, name string) (clusterSetupResult, error) {
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/organizations/%s/clusters", orgID), map[string]any{"name": name})
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

func (c *scenarioClient) createProject(ctx context.Context, orgID uuid.UUID, name string) (projectSetupResult, error) {
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/organizations/%s/projects", orgID), map[string]any{"name": name})
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
		"name":       name,
		"namespace":  namespace,
		"cluster_id": clusterID,
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

func (c *scenarioClient) provideDepInput(ctx context.Context, deploymentID uuid.UUID, depName string, managed bool, providerInputs map[string]any) error {
	body := map[string]any{
		"managed":         managed,
		"provider_inputs": providerInputs,
	}
	resp, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/deployments/%s/dependencies/%s/input", deploymentID, depName), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return readBodyError(resp)
	}
	return nil
}

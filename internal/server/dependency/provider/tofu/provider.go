package tofu

import (
	"context"
	"fmt"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
)

// Provider implements ManagedProvider using OpenTofu for provisioning.
type Provider struct {
	runner TofuRunner
}

// TofuRunner wraps OpenTofu CLI operations for the provider.
type TofuRunner interface {
	Init(ctx context.Context, workDir string) error
	Apply(ctx context.Context, workDir string, vars map[string]string) error
	Output(ctx context.Context, workDir string) (map[string]any, error)
	Destroy(ctx context.Context, workDir string, vars map[string]string) error
}

func NewProvider(runner TofuRunner) *Provider {
	return &Provider{runner: runner}
}

func (p *Provider) InputFields() []domain.FieldSpec {
	return []domain.FieldSpec{
		{Name: "module_path", Description: "Path to the OpenTofu module", Required: true},
	}
}

func (p *Provider) Apply(ctx context.Context, req dependency.ProviderRequest) (map[string]any, error) {
	modulePath, ok := req.Inputs["module_path"].(string)
	if !ok {
		return nil, fmt.Errorf("module_path input is required")
	}

	if err := p.runner.Init(ctx, modulePath); err != nil {
		return nil, fmt.Errorf("tofu init: %w", err)
	}

	vars := make(map[string]string)
	for k, v := range req.Inputs {
		if k == "module_path" {
			continue
		}
		vars[k] = fmt.Sprintf("%v", v)
	}

	if err := p.runner.Apply(ctx, modulePath, vars); err != nil {
		return nil, fmt.Errorf("tofu apply: %w", err)
	}

	outputs, err := p.runner.Output(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("tofu output: %w", err)
	}

	return outputs, nil
}

func (p *Provider) Destroy(ctx context.Context, req dependency.ProviderRequest) error {
	modulePath, ok := req.Inputs["module_path"].(string)
	if !ok {
		return fmt.Errorf("module_path input is required")
	}

	vars := make(map[string]string)
	for k, v := range req.Inputs {
		if k == "module_path" {
			continue
		}
		vars[k] = fmt.Sprintf("%v", v)
	}

	if err := p.runner.Destroy(ctx, modulePath, vars); err != nil {
		return fmt.Errorf("tofu destroy: %w", err)
	}

	return nil
}

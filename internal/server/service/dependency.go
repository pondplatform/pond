package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
)

type dependencyService struct {
	specs dependency.SpecRegistry
}

func NewDependencyService(specs dependency.SpecRegistry) DependencyService {
	return &dependencyService{specs: specs}
}

// buildTofuPayload constructs the JSON payload for a tofu.apply command.
func buildTofuPayload(serviceName, depName, depType string, depConfig map[string]any, providerInputs map[string]any) ([]byte, error) {
	return json.Marshal(struct {
		WorkDir   string         `json:"workDir"`
		StatePath string         `json:"statePath"`
		Vars      map[string]any `json:"vars"`
	}{
		WorkDir:   fmt.Sprintf("providers/%s/terraform.tfstate", depType),
		StatePath: fmt.Sprintf("states/%s/%s/terraform.tfstate", serviceName, depName),
		Vars: map[string]any{
			"service_name":        serviceName,
			"dependency_name":     depName,
			"dependency_config":   depConfig,
			"provider_user_input": providerInputs,
		},
	})
}

// ScheduleCommands creates dependency_deployments rows for every dependency
// declared in the deployment's service config snapshot. Depending on whether
// previous config exists and the managed flag, deps are created in different states:
// - awaiting_input: new dependency with no previous config
// - pending: managed dependency with tofu.apply command queued
// - succeeded: non-managed dependency (user_config used as output)
//
// Returns all created dependency deployments for the caller to decide what events to publish.
func (s *dependencyService) ScheduleCommands(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, dep *domain.Deployment) ([]domain.DependencyDeployment, error) {
	var deps []domain.DependencyDeployment

	for depName := range dep.ServiceConfigSnapshot.Dependencies {
		depCfg, err := s.scheduleDependency(ctx, tx, service, environment, dep, depName)
		if err != nil {
			return nil, err
		}
		if depCfg != nil {
			deps = append(deps, *depCfg)
		}
	}

	return deps, nil
}

func (s *dependencyService) scheduleDependency(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, deployment *domain.Deployment, dependencyName string) (*domain.DependencyDeployment, error) {
	var previousConfig *domain.DependencyDeployment = nil
	var err error
	if service.CurrentDeploymentID != nil {
		previousConfig, err = tx.DeploymentInfo.GetDepConfig(ctx, *service.CurrentDeploymentID, dependencyName)
		if err != nil {
			return nil, err
		}
	}

	dep := deployment.ServiceConfigSnapshot.Dependencies[dependencyName]
	var cfg domain.DependencyDeployment
	if previousConfig == nil {
		cfg = domain.DependencyDeployment{
			ID:             uuid.New(),
			DeploymentId:   deployment.ID,
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        nil,
			ProviderInputs: map[string]any{},
			UserConfig:     map[string]any{},
			Status:         domain.DependencyDeploymentStatusAwaitingInput,
			CommandID:      nil,
			Output:         nil,
			CompletedAt:    nil,
		}

	} else if !*previousConfig.Managed {
		// Non-managed dependency with previous config: created with status=pending and
		// no command. When user input is later provided via ScheduleAfterInput, it will
		// be marked succeeded immediately with user_config as outputs (no tofu command).
		cfg = domain.DependencyDeployment{
			ID:             uuid.New(),
			DeploymentId:   deployment.ID,
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        previousConfig.Managed,
			UserConfig:     previousConfig.UserConfig,
			Status:         domain.DependencyDeploymentStatusPending,
			CommandID:      nil,
		}
	} else {
		payload, err := buildTofuPayload(
			deployment.ServiceConfigSnapshot.Name,
			dependencyName,
			dep.Type,
			dep.Config,
			previousConfig.ProviderInputs,
		)
		if err != nil {
			return nil, fmt.Errorf("marshal tofu payload for %q: %w", dependencyName, err)
		}

		now := time.Now()
		cmd := &domain.Command{
			ID:           uuid.New(),
			ClusterID:    environment.ClusterID,
			DeploymentID: deployment.ID,
			Type:         domain.CommandTypeTofuApply,
			Payload:      payload,
			Status:       domain.CommandStatusQueued,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		managed := true
		cfg = domain.DependencyDeployment{
			ID:             uuid.New(),
			DeploymentId:   deployment.ID,
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        &managed,
			UserConfig:     previousConfig.UserConfig,
			Status:         domain.DependencyDeploymentStatusPending,
			CommandID:      &cmd.ID,
		}

		if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
			return nil, fmt.Errorf("create tofu command for dep %q: %w", dependencyName, err)
		}
	}

	if err := tx.DeploymentInfo.CreateDepConfig(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("create dep config for %q: %w", dependencyName, err)
	}
	return &cfg, nil
}

// BuildContexts unmarshals raw dependency outputs into a name→values map for
// helm value generation. All deps — managed (tofu outputs) and non-managed
// (user_config, pre-seeded at scheduling time) — are present in rawOutputs.
func (s *dependencyService) BuildContexts(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error) {
	contexts := make(map[string]map[string]any, len(rawOutputs))
	for depName, raw := range rawOutputs {
		var vals map[string]any
		if err := json.Unmarshal(raw, &vals); err != nil {
			return nil, fmt.Errorf("unmarshal outputs for %q: %w", depName, err)
		}
		contexts[depName] = vals
	}
	return contexts, nil
}

// ScheduleAfterInput processes a dependency after user input is provided.
// For managed deps, creates tofu.apply command and returns it.
// For non-managed deps, marks succeeded immediately with user config as outputs.
func (s *dependencyService) ScheduleAfterInput(ctx context.Context, tx TxRepos, deployment *domain.Deployment, env *domain.Environment, depName string) (*domain.DependencyDeployment, error) {
	cfg, err := tx.DeploymentInfo.GetDepConfig(ctx, deployment.ID, depName)
	if err != nil {
		return nil, fmt.Errorf("get dep config: %w", err)
	}

	dep := deployment.ServiceConfigSnapshot.Dependencies[depName]

	if !*cfg.Managed {
		// Non-managed: mark succeeded immediately with user_config as output
		outputJSON, err := json.Marshal(cfg.UserConfig)
		if err != nil {
			return nil, fmt.Errorf("marshal user config as output: %w", err)
		}
		if err := tx.DeploymentInfo.MarkDepConfigSucceeded(ctx, deployment.ID, depName, outputJSON); err != nil {
			return nil, fmt.Errorf("mark dep config succeeded: %w", err)
		}
		cfg.Status = domain.DependencyDeploymentStatusSucceeded
		cfg.Output = outputJSON
		return cfg, nil
	}

	// Managed: create tofu.apply command
	payload, err := buildTofuPayload(
		deployment.ServiceConfigSnapshot.Name,
		depName,
		dep.Type,
		dep.Config,
		cfg.ProviderInputs,
	)
	if err != nil {
		return nil, fmt.Errorf("marshal tofu payload for %q: %w", depName, err)
	}

	now := time.Now()
	cmd := &domain.Command{
		ID:           uuid.New(),
		ClusterID:    env.ClusterID,
		DeploymentID: deployment.ID,
		Type:         domain.CommandTypeTofuApply,
		Payload:      payload,
		Status:       domain.CommandStatusQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
		return nil, fmt.Errorf("create tofu command for dep %q: %w", depName, err)
	}

	// Update the dep config with the command ID and set status to pending
	cfg.CommandID = &cmd.ID
	cfg.Status = domain.DependencyDeploymentStatusPending

	// Note: We need a method to update just the command_id and status
	// For now, we'll use a direct update approach
	if err := tx.DeploymentInfo.SetDepConfigCommand(ctx, deployment.ID, depName, cmd.ID); err != nil {
		return nil, fmt.Errorf("set dep config command: %w", err)
	}

	return cfg, nil
}

// AdvanceOnResult handles a tofu.apply result: marks the dep succeeded/failed,
// cancels sibling commands on failure, and returns whether all deps are now complete.
func (s *dependencyService) AdvanceOnResult(ctx context.Context, tx TxRepos, deploymentID uuid.UUID, cfg *domain.DependencyDeployment, success bool, output json.RawMessage) (allComplete bool, err error) {
	if !success {
		if err := tx.DeploymentInfo.MarkDepConfigFailed(ctx, deploymentID, cfg.DependencyName); err != nil {
			return false, fmt.Errorf("mark dep config failed: %w", err)
		}
		if err := tx.DeploymentInfo.UpdateCommandsByDeployment(ctx, deploymentID, domain.CommandStatusQueued, domain.CommandStatusCancelled); err != nil {
			return false, fmt.Errorf("cancel sibling commands for deployment %s: %w", deploymentID, err)
		}
		return false, nil
	}

	if err := tx.DeploymentInfo.MarkDepConfigSucceeded(ctx, deploymentID, cfg.DependencyName, output); err != nil {
		return false, fmt.Errorf("mark dep config succeeded: %w", err)
	}

	// AllDepConfigsComplete runs in the same tx so it sees the MarkDepConfigSucceeded
	// row above, eliminating the TOCTOU race between concurrent dependency completions.
	allSucceeded, anyFailed, err := tx.DeploymentInfo.AllDepConfigsComplete(ctx, deploymentID)
	if err != nil {
		return false, fmt.Errorf("check all complete: %w", err)
	}

	return allSucceeded && !anyFailed, nil
}

// Validate checks that all declared dependency types are known.
func (s *dependencyService) Validate(ctx context.Context, deps map[string]domain.DependencyDeclaration) error {
	var errs domain.ValidationErrors

	for name, decl := range deps {
		if !s.specs.Exists(decl.Type) {
			errs.Add("dependency", name, fmt.Sprintf("unknown dependency type %q", decl.Type))
		}
	}

	if errs.HasErrors() {
		return &errs
	}
	return nil
}

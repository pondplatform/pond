package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

func NewDependencyService(specs dependency.SpecRegistry, envs store.EnvironmentRepository) DependencyService {
	return &dependencyService{specs: specs, envs: envs}
}

type dependencyService struct {
	specs dependency.SpecRegistry
	envs  store.EnvironmentRepository
}

func (s *dependencyService) CreateDependencyDeployments(ctx context.Context, tx TxRepos, service *domain.Service, dep *domain.Deployment) (domain.DependencyDeploymentStatus, error) {
	deps := make([]domain.DependencyDeployment, 0, len(dep.ServiceConfigSnapshot.Dependencies))

	for depName := range dep.ServiceConfigSnapshot.Dependencies {
		cfg, err := s.createDependencyDeployment(ctx, tx, service, dep, depName)
		if err != nil {
			return "", err
		}
		deps = append(deps, *cfg)
	}

	return s.computeStatus(deps), nil
}

func (s *dependencyService) createDependencyDeployment(ctx context.Context, tx TxRepos, service *domain.Service, deployment *domain.Deployment, dependencyName string) (*domain.DependencyDeployment, error) {
	var previousConfig *domain.DependencyDeployment
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
	} else {
		cfg = domain.DependencyDeployment{
			ID:             uuid.New(),
			DeploymentId:   deployment.ID,
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        previousConfig.Managed,
			ProviderInputs: previousConfig.ProviderInputs,
			UserConfig:     previousConfig.UserConfig,
			Status:         domain.DependencyDeploymentStatusPending,
			CommandID:      nil,
			Output:         previousConfig.Output,
			CompletedAt:    nil,
		}
	}

	if err := tx.DeploymentInfo.CreateDepConfig(ctx, &cfg); err != nil {
		return nil, fmt.Errorf("create dep config %q: %w", dependencyName, err)
	}

	return &cfg, nil
}

func (s *dependencyService) DependencyDeploymentStatus(ctx context.Context, tx TxRepos, deploymentId uuid.UUID) (domain.DependencyDeploymentStatus, error) {
	deps, err := tx.DeploymentInfo.ListDepConfigs(ctx, deploymentId)
	if err != nil {
		return "", fmt.Errorf("list dep configs: %w", err)
	}

	return s.computeStatus(deps), nil
}

func (s *dependencyService) computeStatus(deps []domain.DependencyDeployment) domain.DependencyDeploymentStatus {
	for _, d := range deps {
		if d.Status == domain.DependencyDeploymentStatusFailed {
			return domain.DependencyDeploymentStatusFailed
		}
	}
	for _, d := range deps {
		if d.Status == domain.DependencyDeploymentStatusAwaitingInput {
			return domain.DependencyDeploymentStatusAwaitingInput
		}
	}
	for _, d := range deps {
		if d.Status == domain.DependencyDeploymentStatusPending {
			return domain.DependencyDeploymentStatusPending
		}
	}
	return domain.DependencyDeploymentStatusSucceeded
}

func (s *dependencyService) ScheduleCommands(ctx context.Context, tx TxRepos, deploymentId uuid.UUID) error {
	dep, err := tx.DeploymentInfo.GetByID(ctx, deploymentId)
	if err != nil {
		return fmt.Errorf("get deployment: %w", err)
	}

	env, err := s.envs.GetByID(ctx, dep.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}

	depConfigs, err := tx.DeploymentInfo.ListDepConfigs(ctx, deploymentId)
	if err != nil {
		return fmt.Errorf("list dep configs: %w", err)
	}

	for _, cfg := range depConfigs {
		if cfg.Status != domain.DependencyDeploymentStatusPending {
			continue
		}

		if cfg.Managed == nil {
			return fmt.Errorf("dep %q has pending status but no managed flag set", cfg.DependencyName)
		}

		if !*cfg.Managed {
			outputJSON, err := json.Marshal(cfg.UserConfig)
			if err != nil {
				return fmt.Errorf("marshal user config for dep %q: %w", cfg.DependencyName, err)
			}
			if err := tx.DeploymentInfo.MarkDepConfigSucceeded(ctx, deploymentId, cfg.DependencyName, outputJSON); err != nil {
				return fmt.Errorf("mark dep %q succeeded: %w", cfg.DependencyName, err)
			}
			continue
		}

		decl := dep.ServiceConfigSnapshot.Dependencies[cfg.DependencyName]
		payload, err := buildTofuPayload(
			dep.ServiceConfigSnapshot.Name,
			cfg.DependencyName,
			decl.Type,
			decl.Config,
			cfg.ProviderInputs,
			environmentProviderInput(env),
		)
		if err != nil {
			return fmt.Errorf("build tofu payload for dep %q: %w", cfg.DependencyName, err)
		}

		now := time.Now()
		cmd := &domain.Command{
			ID:           uuid.New(),
			ClusterID:    env.ClusterID,
			DeploymentID: deploymentId,
			Type:         domain.CommandTypeTofuApply,
			Payload:      payload,
			Status:       domain.CommandStatusQueued,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
			return fmt.Errorf("create tofu command for dep %q: %w", cfg.DependencyName, err)
		}
		if err := tx.DeploymentInfo.SetDepConfigCommand(ctx, deploymentId, cfg.DependencyName, cmd.ID); err != nil {
			return fmt.Errorf("set dep config command for %q: %w", cfg.DependencyName, err)
		}
	}

	return nil
}

func (s *dependencyService) HandleCommandResult(ctx context.Context, tx TxRepos, commandID uuid.UUID) error {
	cmd, err := tx.DeploymentInfo.GetCommand(ctx, commandID)
	if err != nil {
		return fmt.Errorf("get command: %w", err)
	}

	deploymentID, cfg, err := tx.DeploymentInfo.GetDepConfigByCommandID(ctx, commandID)
	if err != nil {
		return fmt.Errorf("get dep config by command id: %w", err)
	}
	if cfg == nil {
		return nil
	}

	if cmd.Status == domain.CommandStatusFailed {
		if err := tx.DeploymentInfo.MarkDepConfigFailed(ctx, deploymentID, cfg.DependencyName); err != nil {
			return fmt.Errorf("mark dep config failed: %w", err)
		}
		if err := tx.DeploymentInfo.UpdateCommandsByDeployment(ctx, deploymentID, domain.CommandStatusQueued, domain.CommandStatusCancelled); err != nil {
			return fmt.Errorf("cancel sibling commands: %w", err)
		}
	} else {
		if err := tx.DeploymentInfo.MarkDepConfigSucceeded(ctx, deploymentID, cfg.DependencyName, cmd.Output); err != nil {
			return fmt.Errorf("mark dep config succeeded: %w", err)
		}
	}

	return nil
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

// Validate checks that all declared dependency types are known.
func (s *dependencyService) Validate(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error {
	var errs api.ValidationErrors

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

func environmentProviderInput(environment *domain.Environment) map[string]any {
	result := make(map[string]any)
	result["name"] = environment.Name
	result["namespace"] = environment.Namespace
	result["defaultIngressBaseHost"] = environment.DefaultIngressBaseHost
	return result
}

// buildTofuPayload constructs the JSON payload for a tofu.apply command.
func buildTofuPayload(serviceName, depName, depType string, depConfig map[string]any, providerInputs map[string]any, environmentInputs map[string]any) ([]byte, error) {
	return wire.MarshalPayload(wire.TofuApplyPayload{
		WorkDir:   fmt.Sprintf("/opt/pond/tofu-providers/%s", depType),
		StatePath: fmt.Sprintf("/opt/pond/states/%s/%s/terraform.tfstate", serviceName, depName),
		Vars: map[string]any{
			"service_name":        serviceName,
			"dependency_name":     depName,
			"dependency_config":   defaultEmptyMap(depConfig),
			"provider_user_input": defaultEmptyMap(providerInputs),
			"environment_config":  defaultEmptyMap(environmentInputs),
		},
	})
}

func defaultEmptyMap(value map[string]any) map[string]any {
	if value == nil {
		return make(map[string]any)
	}
	return value
}

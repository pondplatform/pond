package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/store"
)

type dependencyService struct {
	depConfigs store.DependencyConfigRepository
	specs      dependency.SpecRegistry
}

func NewDependencyService(
	depConfigs store.DependencyConfigRepository,
	specs dependency.SpecRegistry,
) DependencyService {
	return &dependencyService{
		depConfigs: depConfigs,
		specs:      specs,
	}
}

// ScheduleCommands queues a tofu.apply command for every managed dependency
// declared in the deployment's service config snapshot and creates the
// corresponding dependency_deployments rows — all within the provided transaction.
// Non-managed deps are also recorded with status=succeeded and output=user_config
// so that GetDepOutputsByDeployment returns all dep outputs uniformly.
func (s *dependencyService) ScheduleCommands(ctx context.Context, tx TxRepos, dep *domain.Deployment, clusterID uuid.UUID) ([]PendingDep, error) {
	var pending []PendingDep

	for depName, decl := range dep.ServiceConfigSnapshot.Dependencies {
		cfg, err := s.depConfigs.Get(ctx, dep.ServiceID, dep.EnvironmentID, depName)
		if err != nil {
			return nil, fmt.Errorf("get dep config for %q: %w", depName, err)
		}

		if !cfg.Managed {
			userConfigJSON, err := json.Marshal(cfg.UserConfig)
			if err != nil {
				return nil, fmt.Errorf("marshal user config for non-managed dep %q: %w", depName, err)
			}
			nonManagedRow := &domain.DeploymentDependencyConfig{
				ID:             uuid.New(),
				DependencyName: depName,
				DependencyType: decl.Type,
				Managed:        false,
				UserConfig:     cfg.UserConfig,
				Status:         domain.DependencyRequestStatusSucceeded,
				Output:         json.RawMessage(userConfigJSON),
			}
			if err := tx.DeploymentInfo.CreateDepConfig(ctx, dep.ID, nonManagedRow); err != nil {
				return nil, fmt.Errorf("create dep config for non-managed dep %q: %w", depName, err)
			}
			continue
		}

		workDir, _ := cfg.ProviderInputs["workDir"].(string)

		payload, err := json.Marshal(struct {
			WorkDir   string         `json:"workDir"`
			StatePath string         `json:"statePath"`
			Vars      map[string]any `json:"vars"`
		}{
			WorkDir:   workDir,
			StatePath: fmt.Sprintf("states/%s/%s/terraform.tfstate", dep.ServiceConfigSnapshot.Name, depName),
			Vars: map[string]any{
				"service_name":        dep.ServiceConfigSnapshot.Name,
				"dependency_name":     depName,
				"dependency_config":   decl.Config,
				"provider_user_input": cfg.ProviderInputs,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal tofu payload for %q: %w", depName, err)
		}

		now := time.Now()
		cmd := &domain.Command{
			ID:           uuid.New(),
			ClusterID:    clusterID,
			DeploymentID: dep.ID,
			Type:         "tofu.apply",
			Payload:      payload,
			Status:       domain.CommandStatusQueued,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		depCfg := &domain.DeploymentDependencyConfig{
			ID:             uuid.New(),
			DependencyName: depName,
			DependencyType: decl.Type,
			Managed:        true,
			UserConfig:     cfg.UserConfig,
			Status:         domain.DependencyRequestStatusPending,
			CommandID:      &cmd.ID,
		}

		if err := tx.DeploymentInfo.CreateDepConfig(ctx, dep.ID, depCfg); err != nil {
			return nil, fmt.Errorf("create dep config for dep %q: %w", depName, err)
		}
		if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
			return nil, fmt.Errorf("create tofu command for dep %q: %w", depName, err)
		}

		pending = append(pending, PendingDep{Cmd: cmd, DepCfg: depCfg})
	}

	return pending, nil
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

// Validate checks that all declared dependency types are known and that each
// dependency is configured for the given (service, environment) pair.
func (s *dependencyService) Validate(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error {
	var errs domain.ValidationErrors

	for name, decl := range deps {
		if !s.specs.Exists(decl.Type) {
			errs.Add("dependency", name, fmt.Sprintf("unknown dependency type %q", decl.Type))
			continue
		}

		_, err := s.depConfigs.Get(ctx, serviceID, envID, name)
		if err != nil {
			errs.Add("dependency", name, "dependency not configured for this environment")
		}
	}

	if errs.HasErrors() {
		return &errs
	}
	return nil
}

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/events"
)

type dependencyService struct {
	specs dependency.SpecRegistry
}

func NewDependencyService(specs dependency.SpecRegistry) DependencyService {
	return &dependencyService{specs: specs}
}

// ScheduleCommands queues a tofu.apply command for every managed dependency
// declared in the deployment's service config snapshot and creates the
// corresponding dependency_deployments rows — all within the provided transaction.
// Non-managed deps are also recorded with status=succeeded and output=user_config
// so that GetDepOutputsByDeployment returns all dep outputs uniformly.
func (s *dependencyService) ScheduleCommands(ctx context.Context, tx TxRepos, service *domain.Service,  environment *domain.Environment, dep *domain.Deployment) ([]domain.DeploymentDependencyConfig, error) {
	var pending []domain.DeploymentDependencyConfig

	for depName, _ := range dep.ServiceConfigSnapshot.Dependencies {
		dependency, err := s.scheduleDependency(ctx, tx, service,environment, dep, depName)
		if err != nil {
			return  nil, err
		} else if dependency!= nil{

		}
	}

	return pending, nil
}

func (s *dependencyService) scheduleDependency(ctx context.Context, tx TxRepos, service *domain.Service, environment *domain.Environment, deployment *domain.Deployment, dependencyName string) (*domain.DeploymentDependencyConfig, error) {
	var previousConfig *domain.DeploymentDependencyConfig = nil
	var err error
	if service.CurrentDeploymentID != nil {
		previousConfig, err = tx.DeploymentInfo.GetDepConfig(ctx, *service.CurrentDeploymentID, dependencyName)
		if err!= nil {
			return nil, err
		}
	}


	dep := deployment.ServiceConfigSnapshot.Dependencies[dependencyName]
	var cfg domain.DeploymentDependencyConfig
	if previousConfig == nil {
		cfg = domain.DeploymentDependencyConfig {
			ID:             uuid.New(),
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        nil,
			ProviderInputs: map[string]any{},
			UserConfig:     map[string]any{},
			Outputs:        map[string]any{},
			Status:         domain.DependencyRequestAwaitingInput,
			CommandID:      nil,
			Output:         nil,
			CompletedAt:    nil,
		}

	} else if !*previousConfig.Managed {
		cfg = domain.DeploymentDependencyConfig{
			ID:             uuid.New(),
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        previousConfig.Managed,
			UserConfig:     previousConfig.UserConfig,
			Status:         domain.DependencyRequestStatusPending,
			CommandID:      nil,
		}
	} else {
		payload, err := json.Marshal(struct {
			WorkDir   string         `json:"workDir"`
			StatePath string         `json:"statePath"`
			Vars      map[string]any `json:"vars"`
		}{
			WorkDir:   fmt.Sprintf("providers/%s/terraform.tfstate", dep.Type),
			StatePath: fmt.Sprintf("states/%s/%s/terraform.tfstate", deployment.ServiceConfigSnapshot.Name, dependencyName),
			Vars: map[string]any{
				"service_name":        deployment.ServiceConfigSnapshot.Name,
				"dependency_name":     dependencyName,
				"dependency_config":   dep.Config,
				"provider_user_input": previousConfig.ProviderInputs,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal tofu payload for %q: %w", dependencyName, err)
		}

		now := time.Now()
		cmd := &domain.Command{
			ID:           uuid.New(),
			ClusterID:    environment.ClusterID,
			DeploymentID: deployment.ID,
			Type:         "tofu.apply",
			Payload:      payload,
			Status:       domain.CommandStatusQueued,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		managed := true
		cfg = domain.DeploymentDependencyConfig{
			ID:             uuid.New(),
			DependencyName: dependencyName,
			DependencyType: dep.Type,
			Managed:        &managed,
			UserConfig:     previousConfig.UserConfig,
			Status:         domain.DependencyRequestStatusPending,
			CommandID:      &cmd.ID,
		}

		if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
			return nil, fmt.Errorf("create tofu command for dep %q: %w", dependencyName, err)
		}
	}
	tx.DeploymentInfo.CreateDepConfig(ctx, &cfg)
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

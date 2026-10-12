package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/events"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type internalDependencyService struct {
	specs          dependency.SpecRegistry
	envs           store.EnvironmentRepository
	deploymentInfo store.DeploymentInfoStore
	tx             Transactor
	topics         events.Topics
}

// Validate checks that all declared dependency types are known.
func (s *internalDependencyService) Validate(ctx context.Context, deps map[string]serviceconfig.DependencyDeclaration) error {
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

// ScheduleCommand completes an unmanaged dependency on the spot and queues a
// tofu.apply command for a managed one.
func (s *internalDependencyService) ScheduleCommand(ctx context.Context, deployment db.Deployment, dependency db.DependencyDeployment) error {
	if dependency.Managed == nil {
		return fmt.Errorf("dependency %q is pending but has no managed flag", dependency.DependencyName)
	}
	if !*dependency.Managed {
		return s.completeUnmanaged(ctx, dependency)
	}

	env, err := s.envs.GetByID(ctx, deployment.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}
	cmd, err := newTofuApplyCommand(deployment, *env, dependency)
	if err != nil {
		return err
	}
	return s.enqueueTofuApply(ctx, cmd, dependency.DependencyName)
}

func (s *internalDependencyService) completeUnmanaged(ctx context.Context, dependency db.DependencyDeployment) error {
	output, err := json.Marshal(dependency.UserConfig)
	if err != nil {
		return fmt.Errorf("marshal user config for dependency %q: %w", dependency.DependencyName, err)
	}
	if err := s.deploymentInfo.MarkDepConfigSucceeded(ctx, dependency.DeploymentId, dependency.DependencyName, output); err != nil {
		return fmt.Errorf("mark dependency %q succeeded: %w", dependency.DependencyName, err)
	}
	return nil
}

// enqueueTofuApply links the command to its dependency in the same transaction
// that creates it, so an agent can never pick up a command whose result would
// find no dependency row.
func (s *internalDependencyService) enqueueTofuApply(ctx context.Context, cmd *db.Command, dependencyName string) error {
	err := s.tx.RunInTx(ctx, func(ctx context.Context, tx TxRepos) error {
		if err := tx.DeploymentInfo.CreateCommand(ctx, cmd); err != nil {
			return fmt.Errorf("create tofu command for dependency %q: %w", dependencyName, err)
		}
		if err := tx.DeploymentInfo.SetDepConfigCommand(ctx, cmd.DeploymentID, dependencyName, cmd.ID); err != nil {
			return fmt.Errorf("link tofu command to dependency %q: %w", dependencyName, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return notifyCommandQueued(ctx, s.topics, cmd)
}

func newTofuApplyCommand(deployment db.Deployment, env db.Environment, dependency db.DependencyDeployment) (*db.Command, error) {
	declaration := deployment.ServiceConfigSnapshot.Dependencies[dependency.DependencyName]
	payload, err := buildTofuPayload(
		deployment.EnvironmentID,
		deployment.ServiceConfigSnapshot.Name,
		dependency.DependencyName,
		declaration.Type,
		declaration.Config,
		dependency.ProviderInputs,
		environmentProviderInput(&env),
	)
	if err != nil {
		return nil, fmt.Errorf("build tofu payload for dependency %q: %w", dependency.DependencyName, err)
	}
	return newCommand(env.ClusterID, deployment.ID, db.CommandTypeTofuApply, payload), nil
}

// newDependencyDeployments builds one row per declared dependency, sorted by
// name. An input wins over the previous deployment's configuration.
func newDependencyDeployments(deployment db.Deployment, inputs map[string]api.DependencyInput, previous map[string]db.DependencyDeployment) []db.DependencyDeployment {
	declared := deployment.ServiceConfigSnapshot.Dependencies
	dependencies := make([]db.DependencyDeployment, 0, len(declared))
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		dependency := awaitingInputDependency(deployment.ID, name, declared[name].Type)
		if input, ok := inputs[name]; ok {
			dependency = applyInput(dependency, input)
		} else if prev, ok := previous[name]; ok && prev.Managed != nil {
			dependency = carryOver(dependency, prev)
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies
}

func awaitingInputDependency(deploymentID uuid.UUID, name, dependencyType string) db.DependencyDeployment {
	return db.DependencyDeployment{
		ID:             uuid.New(),
		DeploymentId:   deploymentID,
		DependencyName: name,
		DependencyType: dependencyType,
		ProviderInputs: map[string]any{},
		UserConfig:     map[string]any{},
		Status:         db.DependencyDeploymentStatusAwaitingInput,
	}
}

func applyInput(dependency db.DependencyDeployment, input api.DependencyInput) db.DependencyDeployment {
	managed := input.Managed
	dependency.Managed = &managed
	if managed {
		dependency.ProviderInputs = defaultEmptyMap(input.Values)
	} else {
		dependency.UserConfig = defaultEmptyMap(input.Values)
	}
	dependency.Status = db.DependencyDeploymentStatusPending
	return dependency
}

// applyInputs returns all dependencies, with those named in inputs configured.
func applyInputs(dependencies []db.DependencyDeployment, inputs map[string]api.DependencyInput) []db.DependencyDeployment {
	result := make([]db.DependencyDeployment, len(dependencies))
	for i, dependency := range dependencies {
		if input, ok := inputs[dependency.DependencyName]; ok {
			dependency = applyInput(dependency, input)
		}
		result[i] = dependency
	}
	return result
}

func carryOver(dependency, previous db.DependencyDeployment) db.DependencyDeployment {
	dependency.Managed = previous.Managed
	dependency.ProviderInputs = defaultEmptyMap(previous.ProviderInputs)
	dependency.UserConfig = defaultEmptyMap(previous.UserConfig)
	dependency.Status = db.DependencyDeploymentStatusPending
	return dependency
}

// aggregateStatus reduces dependency statuses to one, by precedence:
// failed, awaiting input, pending, succeeded.
func aggregateStatus(dependencies []db.DependencyDeployment) db.DependencyDeploymentStatus {
	for _, status := range []db.DependencyDeploymentStatus{
		db.DependencyDeploymentStatusFailed,
		db.DependencyDeploymentStatusAwaitingInput,
		db.DependencyDeploymentStatusPending,
	} {
		if slices.ContainsFunc(dependencies, func(d db.DependencyDeployment) bool { return d.Status == status }) {
			return status
		}
	}
	return db.DependencyDeploymentStatusSucceeded
}

func initialStatus(dependencies []db.DependencyDeployment) db.DeploymentStatus {
	if aggregateStatus(dependencies) == db.DependencyDeploymentStatusAwaitingInput {
		return api.DeploymentStatusAwaitingInput
	}
	return api.DeploymentStatusPending
}

func buildContexts(rawOutputs map[string]json.RawMessage) (map[string]map[string]any, error) {
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

func environmentProviderInput(environment *db.Environment) map[string]any {
	result := make(map[string]any)
	result["name"] = environment.Name
	result["namespace"] = environment.Namespace
	result["defaultIngressBaseHost"] = environment.DefaultIngressBaseHost
	return result
}

// buildTofuPayload constructs the JSON payload for a tofu.apply command.
func buildTofuPayload(environmentId uuid.UUID, serviceName, depName, depType string, depConfig map[string]any, providerInputs map[string]any, environmentInputs map[string]any) ([]byte, error) {
	return wire.MarshalPayload(wire.TofuApplyPayload{
		WorkDir:   fmt.Sprintf("/opt/pond/tofu-providers/%s", depType),
		StatePath: fmt.Sprintf("/states/%s/%s/%s/terraform.tfstate", environmentId, serviceName, depName),
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

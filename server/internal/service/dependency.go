package service

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/agent/wire"
)

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
	return newCommand(env.ClusterID, deployment.ID, db.CommandTypeTofuApply, phaseDependencies, payload), nil
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

package service

import (
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// resolveInputs gives every declared dependency its input: the request's if it
// has one, otherwise the previous deployment's, as long as the dependency kept
// its type. A dependency left without input is a validation error.
func resolveInputs(declared map[string]serviceconfig.DependencyDeclaration, requested map[string]api.DependencyInput, previous map[string]db.DependencyDeployment) (map[string]api.DependencyInput, error) {
	resolved := make(map[string]api.DependencyInput, len(declared))
	var errs api.ValidationErrors
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if input, ok := requested[name]; ok {
			resolved[name] = input
			continue
		}
		if prev, ok := previous[name]; ok && prev.DependencyType == declared[name].Type {
			resolved[name] = inputOf(prev)
			continue
		}
		errs.Add("dependency", name, "missing input: the request gives none, and the service's previous deployment has none to reuse")
	}
	if errs.HasErrors() {
		return nil, &errs
	}
	return resolved, nil
}

func inputOf(dependency db.DependencyDeployment) api.DependencyInput {
	if dependency.Managed {
		return api.DependencyInput{Managed: true, Values: dependency.ProviderInputs}
	}
	return api.DependencyInput{Managed: false, Values: dependency.UserConfig}
}

// plan builds a deployment's dependency rows and a tofu.apply for each managed
// one. The rollout is not planned here: its command is created only once its
// inputs, the dependencies' outputs, exist.
func plan(deployment db.Deployment, env db.Environment, inputs map[string]api.DependencyInput) ([]db.DependencyDeployment, []*db.Command, error) {
	declared := deployment.ServiceConfigSnapshot.Dependencies
	var dependencies []db.DependencyDeployment
	var commands []*db.Command
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		input := inputs[name]
		dependency := db.DependencyDeployment{
			ID:             uuid.New(),
			DeploymentId:   deployment.ID,
			DependencyName: name,
			DependencyType: declared[name].Type,
			Managed:        input.Managed,
			ProviderInputs: map[string]any{},
			UserConfig:     map[string]any{},
		}
		if input.Managed {
			dependency.ProviderInputs = defaultEmptyMap(input.Values)
			cmd, err := newTofuApplyCommand(deployment, env, dependency)
			if err != nil {
				return nil, nil, err
			}
			dependency.CommandID = &cmd.ID
			commands = append(commands, cmd)
		} else {
			dependency.UserConfig = defaultEmptyMap(input.Values)
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, commands, nil
}

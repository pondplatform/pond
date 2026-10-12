package service

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

// A deployment's commands run in phases. Every dependency is provisioned
// first; the rollout then uses their outputs.
const (
	phaseDependencies = 0
	phaseRollout      = 1
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

// plan builds a deployment's dependency rows and every command it will run: a
// tofu.apply per managed dependency, then the helm rollout. The rollout's
// payload is left for activate.
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
	commands = append(commands, newCommand(env.ClusterID, deployment.ID, db.CommandTypeHelmUpgrade, phaseRollout, nil))
	return dependencies, commands, nil
}

// firstPhase is the lowest phase that has commands.
func firstPhase(commands []*db.Command) int {
	first := commands[0].Phase
	for _, cmd := range commands {
		first = min(first, cmd.Phase)
	}
	return first
}

// activate prepares the commands of a phase that is starting, and returns the
// ones it changed. A rollout is rendered from the outputs of the dependencies,
// which every earlier phase has produced by now.
func activate(deployment db.Deployment, env db.Environment, dependencies []db.DependencyDeployment, commands []*db.Command, phase int, helmGen helmgen.HelmValuesGenerator) ([]*db.Command, error) {
	var changed []*db.Command
	for _, cmd := range commands {
		if cmd.Phase != phase || cmd.Type != db.CommandTypeHelmUpgrade {
			continue
		}
		payload, err := helmUpgradePayload(deployment, env, dependencyOutputs(dependencies, commands), helmGen)
		if err != nil {
			return nil, err
		}
		cmd.Payload = payload
		changed = append(changed, cmd)
	}
	return changed, nil
}

// dependencyOutputs are the values a config template can refer to as
// {{name.key}}: a managed dependency's tofu outputs, an unmanaged one's
// configuration.
func dependencyOutputs(dependencies []db.DependencyDeployment, commands []*db.Command) map[string]json.RawMessage {
	outputs := make(map[string]json.RawMessage, len(dependencies))
	for _, dependency := range dependencies {
		if !dependency.Managed {
			// Values decoded from JSON always encode again.
			outputs[dependency.DependencyName], _ = json.Marshal(defaultEmptyMap(dependency.UserConfig))
			continue
		}
		if cmd := commandByID(commands, dependency.CommandID); cmd != nil && len(cmd.Output) > 0 {
			outputs[dependency.DependencyName] = cmd.Output
		}
	}
	return outputs
}

// outcome is what a finished command means for its deployment.
type outcome struct {
	// failure, when set, fails the deployment with this reason.
	failure string
	// done is set when the deployment has succeeded.
	done bool
	// start, when set, is the phase to activate next.
	start *int
}

// afterResult decides what a finished command leads to. commands holds every
// command of the deployment, the finished one included.
func afterResult(finished *db.Command, commands []*db.Command, dependencies []db.DependencyDeployment) outcome {
	if finished.Status == db.CommandStatusFailed {
		return outcome{failure: commandFailure(describe(finished, dependencies), finished)}
	}
	if finished.Type == db.CommandTypeTofuApply && !isJSONObject(finished.Output) {
		return outcome{failure: fmt.Sprintf("%s: tofu outputs must be a JSON object, got %s",
			describe(finished, dependencies), abbreviate(finished.Output))}
	}
	next := -1
	for _, cmd := range commands {
		if cmd.Phase == finished.Phase && cmd.Status != db.CommandStatusSucceeded {
			return outcome{}
		}
		if cmd.Phase > finished.Phase && (next == -1 || cmd.Phase < next) {
			next = cmd.Phase
		}
	}
	if next == -1 {
		return outcome{done: true}
	}
	return outcome{start: &next}
}

// describe names what a command does, for failure messages.
func describe(cmd *db.Command, dependencies []db.DependencyDeployment) string {
	if cmd.Type == db.CommandTypeHelmUpgrade {
		return "helm upgrade"
	}
	for _, dependency := range dependencies {
		if dependency.CommandID != nil && *dependency.CommandID == cmd.ID {
			return fmt.Sprintf("dependency %q", dependency.DependencyName)
		}
	}
	return cmd.Type
}

func commandFailure(what string, cmd *db.Command) string {
	if cmd.Error == "" {
		return what + " failed"
	}
	return what + " failed: " + cmd.Error
}

func commandByID(commands []*db.Command, id *uuid.UUID) *db.Command {
	if id == nil {
		return nil
	}
	for _, cmd := range commands {
		if cmd.ID == *id {
			return cmd
		}
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func abbreviate(raw json.RawMessage) string {
	const limit = 80
	if len(raw) == 0 {
		return "nothing"
	}
	if len(raw) > limit {
		return string(raw[:limit]) + "…"
	}
	return string(raw)
}

func helmUpgradePayload(deployment db.Deployment, env db.Environment, outputs map[string]json.RawMessage, helmGen helmgen.HelmValuesGenerator) (json.RawMessage, error) {
	contexts, err := buildContexts(outputs)
	if err != nil {
		return nil, err
	}
	cfg, err := renderConfigs(deployment.ServiceConfigSnapshot, contexts)
	if err != nil {
		return nil, err
	}
	values, err := helmValuesYAML(helmGen, &cfg, &env, deployment.ImageTag)
	if err != nil {
		return nil, err
	}
	payload, err := wire.MarshalPayload(wire.HelmUpgradePayload{
		ReleaseName: cfg.Name,
		Namespace:   env.Namespace,
		ChartPath:   "/opt/pond/helm-charts/base-service",
		Values:      values,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal helm upgrade payload: %w", err)
	}
	return payload, nil
}

func helmValuesYAML(helmGen helmgen.HelmValuesGenerator, cfg *serviceconfig.ServiceConfig, env *db.Environment, imageTag string) ([]byte, error) {
	values, err := helmGen.Generate(cfg, env, imageTag)
	if err != nil {
		return nil, fmt.Errorf("generate helm values: %w", err)
	}
	out, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal helm values: %w", err)
	}
	return out, nil
}

// renderConfigs substitutes dependency outputs into every config file's values.
func renderConfigs(cfg serviceconfig.ServiceConfig, contexts map[string]map[string]any) (serviceconfig.ServiceConfig, error) {
	if len(cfg.Configs) == 0 {
		return cfg, nil
	}
	rendered := make(map[string]serviceconfig.ConfigFileSpec, len(cfg.Configs))
	for name, file := range cfg.Configs {
		values, err := renderTemplate(file.Values, contexts, &cfg)
		if err != nil {
			return cfg, fmt.Errorf("render config %q: %w", name, err)
		}
		file.Values = values
		rendered[name] = file
	}
	cfg.Configs = rendered
	return cfg, nil
}

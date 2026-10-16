package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"github.com/pondplatform/pond/shared/serviceconfig/config"
	"gopkg.in/yaml.v3"
)

// deploymentState is where a deployment stands. It is never stored: every
// entry into the state machine computes it from the deployment's stored
// status, its dependencies and its commands.
type deploymentState int

const (
	// stateFinished: the deployment has ended and no agent is running any of
	// its commands.
	stateFinished deploymentState = iota
	// stateStopping: the deployment has ended, but an agent is still running
	// one of its commands. It has been told to cancel.
	stateStopping
	// stateProvisioning: a managed dependency's tofu.apply is queued or
	// running.
	stateProvisioning
	// stateDependencyFailed: a dependency could not be provisioned.
	stateDependencyFailed
	// stateDependenciesReady: every dependency has its outputs, and the
	// rollout has not started.
	stateDependenciesReady
	// stateRollingOut: the helm.upgrade is queued or running.
	stateRollingOut
	// stateRolloutFailed: the helm.upgrade failed.
	stateRolloutFailed
	// stateRolledOut: the helm.upgrade succeeded.
	stateRolledOut
)

func currentState(d db.Deployment, dependencies []db.DependencyDeployment, commands []*db.Command) deploymentState {
	if d.Status != db.DeploymentInProgress {
		for _, cmd := range commands {
			if cmd.Status == db.CommandStatusDispatched {
				return stateStopping
			}
		}
		return stateFinished
	}

	if rollout := rolloutCommand(commands); rollout != nil {
		switch rollout.Status {
		case db.CommandStatusSucceeded:
			return stateRolledOut
		case db.CommandStatusFailed, db.CommandStatusCancelled:
			return stateRolloutFailed
		default:
			return stateRollingOut
		}
	}

	provisioning := false
	for _, dependency := range dependencies {
		cmd := commandByID(commands, dependency.CommandID)
		if dependencyProblem(dependency, cmd) != "" {
			return stateDependencyFailed
		}
		if dependency.Managed && cmd.Status != db.CommandStatusSucceeded {
			provisioning = true
		}
	}
	if provisioning {
		return stateProvisioning
	}
	return stateDependenciesReady
}

// dependencyProblem says why a dependency cannot be used, or returns "" when
// it is ready or still being provisioned. cmd is its tofu.apply.
func dependencyProblem(dependency db.DependencyDeployment, cmd *db.Command) string {
	if !dependency.Managed {
		return ""
	}
	name := dependency.DependencyName
	if cmd == nil {
		return fmt.Sprintf("dependency %q has no tofu.apply command", name)
	}
	switch cmd.Status {
	case db.CommandStatusFailed:
		return commandFailure(fmt.Sprintf("dependency %q failed to provision", name), cmd)
	case db.CommandStatusCancelled:
		return fmt.Sprintf("dependency %q: its tofu.apply was cancelled", name)
	case db.CommandStatusSucceeded:
		if !isJSONObject(cmd.Output) {
			return fmt.Sprintf("dependency %q: tofu outputs must be a JSON object, got %s", name, abbreviate(cmd.Output))
		}
	}
	return ""
}

// stateMachine moves deployments. Its entry points are advance, after
// something about a deployment changed, and cancel. Both work out the current
// state, then take that state's step. Callers hold the deployment's row lock,
// or have just inserted it, in the transaction they pass.
type stateMachine struct {
	helmGen helmgen.HelmValuesGenerator
	now     func() time.Time
}

func (m *stateMachine) advance(ctx context.Context, tx store.Repos, d *db.Deployment) error {
	dependencies, commands, err := load(ctx, tx, d.ID)
	if err != nil {
		return err
	}
	switch currentState(*d, dependencies, commands) {
	case stateFinished, stateStopping, stateProvisioning, stateRollingOut:
		return nil // waiting for agents, or nothing left to do
	case stateDependenciesReady:
		return m.startRollout(ctx, tx, d, dependencies, commands)
	case stateDependencyFailed:
		return m.failDependency(ctx, tx, d, dependencies, commands)
	case stateRolloutFailed:
		return m.failRollout(ctx, tx, d, commands)
	case stateRolledOut:
		return m.completeDeployment(ctx, tx, d)
	}
	return nil
}

func (m *stateMachine) cancel(ctx context.Context, tx store.Repos, d *db.Deployment) error {
	dependencies, commands, err := load(ctx, tx, d.ID)
	if err != nil {
		return err
	}
	switch currentState(*d, dependencies, commands) {
	case stateFinished, stateStopping:
		return api.Conflict("deployment %s has already %s and can no longer be cancelled", d.ID, d.APIStatus())
	}
	return m.end(ctx, tx, d, db.DeploymentCancelled, "")
}

// startRollout queues the helm.upgrade, rendered from the dependencies'
// outputs, and records the exports other services may import.
func (m *stateMachine) startRollout(ctx context.Context, tx store.Repos, d *db.Deployment, dependencies []db.DependencyDeployment, commands []*db.Command) error {
	env, err := tx.Envs.GetByID(ctx, d.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}
	payload, exports, err := helmUpgradePayload(*d, *env, dependencyOutputs(dependencies, commands), m.helmGen)
	if err != nil {
		return m.end(ctx, tx, d, db.DeploymentFailed, err.Error())
	}
	if err := tx.Deployments.SetExports(ctx, d.ID, exports); err != nil {
		return err
	}
	d.Exports = exports
	return tx.Deployments.CreateCommand(ctx, newCommand(env.ClusterID, d.ID, db.CommandTypeHelmUpgrade, payload))
}

func (m *stateMachine) failDependency(ctx context.Context, tx store.Repos, d *db.Deployment, dependencies []db.DependencyDeployment, commands []*db.Command) error {
	for _, dependency := range dependencies {
		if problem := dependencyProblem(dependency, commandByID(commands, dependency.CommandID)); problem != "" {
			return m.end(ctx, tx, d, db.DeploymentFailed, problem)
		}
	}
	return nil
}

func (m *stateMachine) failRollout(ctx context.Context, tx store.Repos, d *db.Deployment, commands []*db.Command) error {
	rollout := rolloutCommand(commands)
	reason := commandFailure("helm upgrade failed", rollout)
	if rollout.Status == db.CommandStatusCancelled {
		reason = "helm upgrade was cancelled"
	}
	return m.end(ctx, tx, d, db.DeploymentFailed, reason)
}

func (m *stateMachine) completeDeployment(ctx context.Context, tx store.Repos, d *db.Deployment) error {
	if err := m.end(ctx, tx, d, db.DeploymentSucceeded, ""); err != nil {
		return err
	}
	return tx.Services.SetRunningDeployment(ctx, d.ServiceID, d.ID)
}

// end gives the deployment its final status. Commands that have not started
// are cancelled at once; one that an agent is running is left to its agent,
// which is told to cancel it and reports back.
func (m *stateMachine) end(ctx context.Context, tx store.Repos, d *db.Deployment, state db.DeploymentState, reason string) error {
	now := m.now()
	d.Status = state
	d.CompletedAt = &now
	if reason != "" {
		d.Error = &reason
	}
	if err := tx.Deployments.SaveDeployment(ctx, d); err != nil {
		return err
	}
	if err := tx.Deployments.UpdateCommandsByDeployment(ctx, d.ID, db.CommandStatusCancelled, db.CommandStatusQueued); err != nil {
		return fmt.Errorf("cancel queued commands: %w", err)
	}
	return nil
}

func load(ctx context.Context, tx store.Repos, deploymentID uuid.UUID) ([]db.DependencyDeployment, []*db.Command, error) {
	dependencies, err := tx.Deployments.ListDepConfigs(ctx, deploymentID)
	if err != nil {
		return nil, nil, fmt.Errorf("list dependencies: %w", err)
	}
	commands, err := tx.Deployments.ListCommandsByDeployment(ctx, deploymentID)
	if err != nil {
		return nil, nil, fmt.Errorf("list commands: %w", err)
	}
	return dependencies, commands, nil
}

func rolloutCommand(commands []*db.Command) *db.Command {
	for _, cmd := range commands {
		if cmd.Type == db.CommandTypeHelmUpgrade {
			return cmd
		}
	}
	return nil
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

func commandFailure(what string, cmd *db.Command) string {
	if cmd.Error == "" {
		return what
	}
	return what + ": " + cmd.Error
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

// dependencyOutputs are the values a template can refer to as
// {{dependencies.<name>.<key>}}: a managed dependency's tofu outputs, an
// unmanaged one's configuration.
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

func helmUpgradePayload(deployment db.Deployment, env db.Environment, outputs map[string]json.RawMessage, helmGen helmgen.HelmValuesGenerator) (json.RawMessage, map[string]string, error) {
	contexts, err := buildContexts(outputs)
	if err != nil {
		return nil, nil, err
	}
	snapshot := deployment.ServiceConfigSnapshot
	templates := newTemplateContext(&snapshot, &env, deployment.Variables).withDependencies(contexts)
	cfg, err := renderServiceConfig(snapshot, &env, templates)
	if err != nil {
		return nil, nil, err
	}
	// The submit could only check what was known then, not a dependency's outputs.
	if err := config.ValidateGateway(cfg.Gateway); err != nil {
		return nil, nil, fmt.Errorf("rendered gateway: %w", err)
	}
	values, err := helmValuesYAML(helmGen, &cfg, &env, deployment.ImageTag, deployment.ID)
	if err != nil {
		return nil, nil, err
	}
	payload, err := wire.MarshalPayload(wire.HelmUpgradePayload{
		ReleaseName: cfg.Name,
		Namespace:   env.Namespace,
		ChartPath:   "/opt/pond/helm-charts/base-service",
		Values:      values,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal helm upgrade payload: %w", err)
	}
	return payload, computeExports(&cfg, &env, deployment.Variables), nil
}

func helmValuesYAML(helmGen helmgen.HelmValuesGenerator, cfg *serviceconfig.ServiceConfig, env *db.Environment, imageTag string, deploymentID uuid.UUID) ([]byte, error) {
	values, err := helmGen.Generate(cfg, env, imageTag, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("generate helm values: %w", err)
	}
	out, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal helm values: %w", err)
	}
	return out, nil
}

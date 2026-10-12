package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/helmgen"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
	"gopkg.in/yaml.v3"
)

// deploymentState is everything decide needs about one deployment, loaded
// under the deployment's row lock.
type deploymentState struct {
	Deployment   db.Deployment
	Environment  db.Environment
	Dependencies []db.DependencyDeployment
	Commands     map[uuid.UUID]*db.Command
}

// decision is what reconciling a deployment changes.
type decision struct {
	Deployment        db.Deployment
	DeploymentChanged bool
	// Dependencies holds only the rows that changed.
	Dependencies []db.DependencyDeployment
	// Commands are new commands to create, queued.
	Commands []*db.Command
	// CancelLive cancels the deployment's queued and dispatched commands.
	CancelLive        bool
	SetServiceRunning bool
}

func isTerminal(status db.DeploymentStatus) bool {
	switch status {
	case api.DeploymentStatusSucceeded, api.DeploymentStatusFailed, api.DeploymentStatusCancelled:
		return true
	}
	return false
}

// decide works out the next step of a deployment from its current state. It
// does no I/O, so calling it again on the state it produced changes nothing.
// A problem with the deployment itself, such as a template that does not
// render, fails the deployment with the reason rather than returning an error.
func decide(s deploymentState, helmGen helmgen.HelmValuesGenerator, now time.Time) decision {
	d := decision{Deployment: s.Deployment}
	if isTerminal(s.Deployment.Status) {
		return d
	}

	dependencies := slices.Clone(s.Dependencies)
	changed := make([]bool, len(dependencies))
	var failure string
	for i := range dependencies {
		var reason string
		changed[i], reason = settleDependency(&dependencies[i], s.Commands, now)
		if failure == "" {
			failure = reason
		}
	}

	switch aggregateStatus(dependencies) {
	case db.DependencyDeploymentStatusFailed:
		if failure == "" {
			failure = "a dependency failed to provision"
		}
		d.fail(failure, now)
	case db.DependencyDeploymentStatusAwaitingInput:
		d.setStatus(api.DeploymentStatusAwaitingInput)
	case db.DependencyDeploymentStatusPending:
		d.setStatus(api.DeploymentStatusPending)
		d.provision(s, dependencies, changed, now)
	case db.DependencyDeploymentStatusSucceeded:
		d.rollOut(s, dependencies, helmGen, now)
	}

	for i, dependency := range dependencies {
		if changed[i] {
			d.Dependencies = append(d.Dependencies, dependency)
		}
	}
	return d
}

// settleDependency completes a pending dependency whose outcome is known: an
// unmanaged one at once, a managed one from its finished command. It reports
// whether the row changed and, when it failed, why.
func settleDependency(dependency *db.DependencyDeployment, commands map[uuid.UUID]*db.Command, now time.Time) (bool, string) {
	if dependency.Status != db.DependencyDeploymentStatusPending {
		return false, ""
	}
	name := dependency.DependencyName
	if dependency.Managed == nil {
		dependency.Status = db.DependencyDeploymentStatusFailed
		return true, fmt.Sprintf("dependency %q is pending but has no managed flag", name)
	}
	if !*dependency.Managed {
		output, err := json.Marshal(defaultEmptyMap(dependency.UserConfig))
		if err != nil {
			dependency.Status = db.DependencyDeploymentStatusFailed
			return true, fmt.Sprintf("dependency %q: encode its configuration: %s", name, err)
		}
		dependency.Status = db.DependencyDeploymentStatusSucceeded
		dependency.Output = output
		dependency.CompletedAt = &now
		return true, ""
	}

	cmd := currentCommand(*dependency, commands)
	if cmd == nil {
		return false, ""
	}
	switch cmd.Status {
	case db.CommandStatusSucceeded:
		if !isJSONObject(cmd.Output) {
			dependency.Status = db.DependencyDeploymentStatusFailed
			return true, fmt.Sprintf("dependency %q: tofu outputs must be a JSON object, got %s", name, abbreviate(cmd.Output))
		}
		dependency.Status = db.DependencyDeploymentStatusSucceeded
		dependency.Output = cmd.Output
		dependency.CompletedAt = &now
		return true, ""
	case db.CommandStatusFailed:
		dependency.Status = db.DependencyDeploymentStatusFailed
		return true, commandFailure(fmt.Sprintf("dependency %q failed to provision", name), cmd)
	case db.CommandStatusCancelled:
		dependency.Status = db.DependencyDeploymentStatusFailed
		return true, fmt.Sprintf("dependency %q: its tofu.apply command was cancelled", name)
	}
	return false, ""
}

// provision queues a tofu.apply for each pending managed dependency that has
// no command in flight. It links the rows only once every command is built, so
// a failure leaves no row pointing at a command that was never created.
func (d *decision) provision(s deploymentState, dependencies []db.DependencyDeployment, changed []bool, now time.Time) {
	commands := make(map[int]*db.Command)
	for i, dependency := range dependencies {
		if dependency.Status != db.DependencyDeploymentStatusPending || !isManaged(dependency) || hasLiveCommand(dependency, s.Commands) {
			continue
		}
		cmd, err := newTofuApplyCommand(s.Deployment, s.Environment, dependency)
		if err != nil {
			d.fail(err.Error(), now)
			return
		}
		commands[i] = cmd
	}
	for i := range dependencies {
		if cmd, ok := commands[i]; ok {
			dependencies[i].CommandID = &cmd.ID
			changed[i] = true
			d.Commands = append(d.Commands, cmd)
		}
	}
}

// rollOut runs helm once every dependency has succeeded, and finishes the
// deployment from its result.
func (d *decision) rollOut(s deploymentState, dependencies []db.DependencyDeployment, helmGen helmgen.HelmValuesGenerator, now time.Time) {
	if s.Deployment.HelmCommandID == nil {
		cmd, err := newHelmUpgradeCommand(s.Deployment, s.Environment, dependencies, helmGen)
		if err != nil {
			d.fail(err.Error(), now)
			return
		}
		d.setStatus(api.DeploymentStatusPending)
		d.Deployment.HelmCommandID = &cmd.ID
		d.DeploymentChanged = true
		d.Commands = append(d.Commands, cmd)
		return
	}

	cmd := s.Commands[*s.Deployment.HelmCommandID]
	if cmd == nil {
		d.fail(fmt.Sprintf("helm.upgrade command %s is missing", *s.Deployment.HelmCommandID), now)
		return
	}
	switch cmd.Status {
	case db.CommandStatusSucceeded:
		d.setStatus(api.DeploymentStatusSucceeded)
		d.Deployment.CompletedAt = &now
		d.SetServiceRunning = true
	case db.CommandStatusFailed:
		d.fail(commandFailure("helm upgrade failed", cmd), now)
	case db.CommandStatusCancelled:
		d.fail("helm.upgrade command was cancelled", now)
	}
}

func (d *decision) setStatus(status db.DeploymentStatus) {
	if d.Deployment.Status != status {
		d.Deployment.Status = status
		d.DeploymentChanged = true
	}
}

// fail ends the deployment. Commands it was about to create are dropped and
// the ones already queued or running are cancelled: they would only waste
// agent time.
func (d *decision) fail(reason string, now time.Time) {
	d.setStatus(api.DeploymentStatusFailed)
	d.Deployment.Error = &reason
	d.Deployment.CompletedAt = &now
	d.DeploymentChanged = true
	d.Commands = nil
	d.CancelLive = true
}

func currentCommand(dependency db.DependencyDeployment, commands map[uuid.UUID]*db.Command) *db.Command {
	if dependency.CommandID == nil {
		return nil
	}
	return commands[*dependency.CommandID]
}

func hasLiveCommand(dependency db.DependencyDeployment, commands map[uuid.UUID]*db.Command) bool {
	cmd := currentCommand(dependency, commands)
	return cmd != nil && (cmd.Status == db.CommandStatusQueued || cmd.Status == db.CommandStatusDispatched)
}

func isManaged(dependency db.DependencyDeployment) bool {
	return dependency.Managed != nil && *dependency.Managed
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func commandFailure(what string, cmd *db.Command) string {
	if cmd.Error == "" {
		return what
	}
	return what + ": " + cmd.Error
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

func newHelmUpgradeCommand(deployment db.Deployment, env db.Environment, dependencies []db.DependencyDeployment, helmGen helmgen.HelmValuesGenerator) (*db.Command, error) {
	outputs := make(map[string]json.RawMessage, len(dependencies))
	for _, dependency := range dependencies {
		if len(dependency.Output) > 0 {
			outputs[dependency.DependencyName] = dependency.Output
		}
	}
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
	return newCommand(env.ClusterID, deployment.ID, db.CommandTypeHelmUpgrade, payload), nil
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

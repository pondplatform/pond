package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/agent/wire"
	"github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// DeleteService tears the service down in every environment it is live in,
// that is where its latest deployment is not a succeeded delete. It creates
// and starts one delete deployment per environment, all in one transaction,
// or none: a deployment of the service in progress in any environment, or a
// service importing it in one of those environments, refuses the delete.
func (s *deploymentService) DeleteService(ctx context.Context, serviceID uuid.UUID, req api.DeleteServiceRequest) ([]db.Deployment, error) {
	var created []db.Deployment
	err := s.tx.RunInTx(ctx, func(ctx context.Context, tx store.Repos) error {
		svc, err := tx.Services.GetByID(ctx, serviceID)
		if err != nil {
			return err
		}
		// Work in progress is refused, never cancelled: cancelling would lock
		// deployment rows while holding the service's.
		if err := refuseWhileInProgress(ctx, tx, svc, nil); err != nil {
			return err
		}
		envNames, err := environmentNames(ctx, tx, svc.ProjectID)
		if err != nil {
			return err
		}

		latest, err := tx.Deployments.ListLatestByProject(ctx, svc.ProjectID)
		if err != nil {
			return fmt.Errorf("list latest deployments: %w", err)
		}
		live := liveDeployments(latest, serviceID)
		if dependents := dependentsOf(svc.Name, live, latest, envNames); len(dependents) > 0 {
			return api.Conflict("service %q is a dependency of %s: delete those first, or remove the dependency from them and redeploy",
				svc.Name, strings.Join(dependents, ", "))
		}

		slices.SortFunc(live, func(a, b db.Deployment) int {
			return strings.Compare(envNames[a.EnvironmentID], envNames[b.EnvironmentID])
		})
		for _, last := range live {
			d, err := s.startDelete(ctx, tx, last, req)
			if err != nil {
				return fmt.Errorf("delete service %q from environment %q: %w", svc.Name, envNames[last.EnvironmentID], err)
			}
			created = append(created, *d)
		}
		if len(created) > 0 {
			return tx.Services.SetCurrentDeployment(ctx, serviceID, created[len(created)-1].ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// startDelete creates the delete deployment that follows last, the latest
// deployment in its environment, with its teardown command, and enters the
// state machine.
func (s *deploymentService) startDelete(ctx context.Context, tx store.Repos, last db.Deployment, req api.DeleteServiceRequest) (*db.Deployment, error) {
	commands, err := tx.Deployments.ListCommandsByServiceEnv(ctx, last.ServiceID, last.EnvironmentID, db.CommandTypeHelmUpgrade, db.CommandTypeTofuApply)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	d := newDeleteDeployment(last, req.TriggeredBy, s.now())
	cmd, err := teardownCommand(d.ID, commands, req.KeepDependencies)
	if err != nil {
		return nil, err
	}
	if err := tx.Deployments.Create(ctx, &d); err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	if cmd != nil {
		if err := tx.Deployments.CreateCommand(ctx, cmd); err != nil {
			return nil, fmt.Errorf("create %s command: %w", cmd.Type, err)
		}
	}
	if err := s.machine.advance(ctx, tx, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func environmentNames(ctx context.Context, tx store.Repos, projectID uuid.UUID) (map[uuid.UUID]string, error) {
	envs, err := tx.Envs.ListByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	names := make(map[uuid.UUID]string, len(envs))
	for _, env := range envs {
		names[env.ID] = env.Name
	}
	return names, nil
}

// liveDeployments picks the service's deployments out of latest, the latest
// deployment of each service in each environment, where it was not torn
// down.
func liveDeployments(latest []db.Deployment, serviceID uuid.UUID) []db.Deployment {
	var live []db.Deployment
	for _, d := range latest {
		if d.ServiceID == serviceID && !d.TornDown() {
			live = append(live, d)
		}
	}
	return live
}

// dependentsOf names the services that import name in an environment where
// it is live: their latest deployment there was not torn down, and its config
// declares a service dependency on name.
func dependentsOf(name string, live, latest []db.Deployment, envNames map[uuid.UUID]string) []string {
	liveIn := make(map[uuid.UUID]uuid.UUID, len(live)) // environment → the service
	for _, d := range live {
		liveIn[d.EnvironmentID] = d.ServiceID
	}
	var dependents []string
	for _, d := range latest {
		serviceID, ok := liveIn[d.EnvironmentID]
		if !ok || d.ServiceID == serviceID || d.TornDown() {
			continue
		}
		for _, dependency := range d.ServiceConfigSnapshot.Dependencies {
			if dependency.Type == serviceconfig.DependencyTypeService && dependency.ServiceName() == name {
				dependents = append(dependents, fmt.Sprintf("%s in %s", d.ServiceConfigSnapshot.Name, envNames[d.EnvironmentID]))
				break
			}
		}
	}
	slices.Sort(dependents)
	return dependents
}

// newDeleteDeployment follows last, keeping its image tag and config so the
// history shows what was deleted. It never carries variables: nothing is
// rendered.
func newDeleteDeployment(last db.Deployment, triggeredBy string, now time.Time) db.Deployment {
	return db.Deployment{
		ID:                    uuid.New(),
		ServiceID:             last.ServiceID,
		EnvironmentID:         last.EnvironmentID,
		Kind:                  db.DeploymentKindDelete,
		ImageTag:              last.ImageTag,
		ServiceConfigSnapshot: last.ServiceConfigSnapshot,
		Status:                db.DeploymentInProgress,
		TriggeredBy:           triggeredBy,
		CreatedAt:             now,
	}
}

// teardownCommand is the service.delete that removes what commands, the
// service's helm.upgrade and tofu.apply commands in one environment, oldest
// first, created. It takes everything from them rather than from the
// environment, whose namespace and cluster may have changed since:
//   - the release, namespace and cluster of the latest helm.upgrade, or the
//     cluster of the latest tofu.apply when there was no rollout;
//   - unless keepDeps, every dependency ever applied on that cluster, by its
//     latest apply. That includes dependencies since removed from pond.yml;
//     destroying one already destroyed destroys nothing.
//
// It returns nil when there is nothing to remove.
func teardownCommand(deploymentID uuid.UUID, commands []*db.Command, keepDeps bool) (*db.Command, error) {
	var rollout, lastApply *db.Command
	for _, cmd := range commands {
		switch cmd.Type {
		case db.CommandTypeHelmUpgrade:
			rollout = cmd
		case db.CommandTypeTofuApply:
			lastApply = cmd
		}
	}
	anchor := rollout
	if anchor == nil {
		anchor = lastApply
	}
	if anchor == nil {
		return nil, nil
	}

	var payload wire.ServiceDeletePayload
	if rollout != nil {
		var upgrade wire.HelmUpgradePayload
		if err := json.Unmarshal(rollout.Payload, &upgrade); err != nil {
			return nil, fmt.Errorf("decode payload of helm.upgrade %s: %w", rollout.ID, err)
		}
		payload.Release = &wire.HelmRelease{ReleaseName: upgrade.ReleaseName, Namespace: upgrade.Namespace}
	}
	if !keepDeps {
		byState := map[string]wire.TofuDestroyPayload{}
		for _, cmd := range commands {
			if cmd.Type != db.CommandTypeTofuApply || cmd.ClusterID != anchor.ClusterID {
				continue
			}
			var apply wire.TofuApplyPayload
			if err := json.Unmarshal(cmd.Payload, &apply); err != nil {
				return nil, fmt.Errorf("decode payload of tofu.apply %s: %w", cmd.ID, err)
			}
			name, _ := apply.Vars["dependency_name"].(string)
			byState[apply.StatePath] = wire.TofuDestroyPayload{Name: name, WorkDir: apply.WorkDir, StatePath: apply.StatePath, Vars: apply.Vars}
		}
		for _, statePath := range slices.Sorted(maps.Keys(byState)) {
			payload.Dependencies = append(payload.Dependencies, byState[statePath])
		}
	}
	if payload.Release == nil && len(payload.Dependencies) == 0 {
		return nil, nil
	}

	raw, err := wire.MarshalPayload(payload)
	if err != nil {
		return nil, err
	}
	return newCommand(anchor.ClusterID, deploymentID, db.CommandTypeServiceDelete, raw), nil
}

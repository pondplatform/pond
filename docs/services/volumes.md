# Volumes

A volume is a directory that keeps its contents across restarts and redeploys. Use one for whatever your app stores on disk: a SQLite database, uploads, a game world, downloaded models.

```yaml
volumes:
  data:
    type: shared-single-node
    mountPath: /data
    size: 10Gi
```

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | string | yes | Which copies of the service mount the volume, and from where. `shared-single-node` is the only type so far: one volume that every copy of the service mounts, attached to one node at a time. See [What `shared-single-node` means](#what-shared-single-node-means). |
| `mountPath` | string | yes | Absolute path of the directory inside the container. |
| `size` | string | yes | How much data you expect: a whole number of `Mi`, `Gi` or `Ti`, e.g. `512Mi` or `10Gi`. It can grow later, but never shrink. |

The name (`data` above) identifies the data. Renaming a volume gives the service a new, empty one, and the old one is kept (see [Removing a volume](#removing-a-volume)). Names are lowercase letters, digits and `-`, start and end with a letter or digit, and are at most 59 characters.

There is no default size: a volume can't shrink, so a guess that turns out too large can never be taken back. There is no default type either. How copies of a service share their data decides how the service can be scaled and deployed, so `pond.yml` says it rather than Pond assuming it.

---

## What `shared-single-node` means

A `shared-single-node` volume is **one volume for the whole service, in each environment. Every copy of the service mounts it, and the cluster attaches it to a single node at a time.** A copy here is a replica, and during a deploy, the old and the new version of the service.

With one replica, the default, that's all there is to it: one copy owns the data. Pond also lets you run several replicas on the same volume, but it can't make that safe for you. Read [More than one replica](#more-than-one-replica) before you raise `deployment.replicas`.

### More than one replica

> **Warning: every replica reads and writes the same files at the same time.** Pond doesn't stop you, and it can't tell when this goes wrong. The usual result is corrupted data, often noticed long after the deploy that caused it. Pond keeps no backup to go back to.

Only raise `deployment.replicas` above `1` if all three of these hold:

1. **Your app supports several instances sharing one data directory.** Most apps that keep state on disk don't: they assume they are the only writer. SQLite databases, game worlds, Git repositories and search indexes all get corrupted when two processes write them. Apps that coordinate through a database or a lock service, such as Nextcloud with Redis file locking, can work. If your app's documentation doesn't say it supports this, assume it doesn't.
2. **Every replica can run on the node that holds the volume.** What happens to a replica placed on another node depends on the environment's storage, so the same `pond.yml` can behave differently in staging and production:
   - **Block storage** (cloud disks, Longhorn and similar): replicas on other nodes never start. They wait on a `Multi-Attach` error, and the deployment fails when the rollout times out.
   - **Node-local storage** (`local-path` on k3s and Rancher Desktop): every replica is placed on the node that holds the data. The extra replicas add capacity, but no protection if that node fails.
   - **Network file storage** (NFS and similar): replicas on several nodes usually work, but Pond doesn't promise it, and the warning above still applies.

   Ask your platform team which kind of storage each environment uses.
3. **You don't need replicas to stay up during deploys.** Every replica stops before any new one starts (see below), so more replicas don't shorten a deploy's downtime.

An [environment override](environments.md) can raise `deployment.replicas` for one environment. The same three conditions then have to hold there.

`replicas: 0` stops the service and keeps its data.

### Deploys stop every old copy first

A service without volumes is deployed as a rolling update: new copies start next to the old ones, and the old ones stop once the new ones are ready. With a single replica, Kubernetes rounds that up to one extra copy and allows no missing ones, so the service stays up throughout.

That moment where two versions of the app run side by side is what a volume can't allow, so a service with volumes is deployed the other way round. Every old copy stops, and only then do the new ones start. Two versions of your app never write the volume at the same time, even if the new one changes the format of its data. Whatever the number of replicas, this has two consequences:
- **Every deploy has a short downtime**: the time your app takes to shut down, plus the time it takes to start.
- **A broken release takes the service down.** If the new version never becomes ready, because it crashes or fails its readiness probe, there is no old copy left to keep serving. The deployment fails after the rollout times out, and the service stays down until you deploy a version that starts. Without volumes, the old copies would have kept running.

> **Warning: a release that doesn't start leaves a service with volumes down.** Try new versions in an environment that doesn't matter first, and keep the last working image tag at hand to deploy again.

Adding or removing a volume is safe either way. The deploy that adds a volume already stops the old copies first. The deploy that removes the last volume goes back to starting new copies first, which is fine because the new copies don't mount the volume.

### What it doesn't share

- **Environments don't share a volume.** Each environment gets its own, so staging and production never see each other's data. Volumes can't be changed per environment either, because a service needs the same storage everywhere (see [Environment Overrides](environments.md)).
- **Services don't share a volume.** A volume belongs to the service that declares it.
- **It isn't storage that several nodes can write at once.** Pond asks the cluster for a volume that one node mounts at a time (`ReadWriteOnce`), which almost every storage system provides. That is the "single node" in the name.

### Types that may come later

`type` exists so that other kinds of volume can be added without changing what `shared-single-node` means. None of these exist yet, and Pond rejects any type but `shared-single-node`:
- **A volume per copy**, for apps that replicate their data themselves. Each copy gets its own volume, and a new copy starts with an empty one, so this only suits apps such as clustered databases.
- **A volume many copies write at once, from any node**, on storage that supports it (NFS, CephFS, EFS). This is what apps built for concurrent access should eventually use instead of several replicas on a `shared-single-node` volume. Whether an environment has such storage would be up to the environment, not `pond.yml`.
- **Temporary space** that doesn't survive a restart, for caches and scratch files.

Moving an existing volume to another type will mean moving its data: the cluster fixes how a volume can be attached when it creates it.

## How a service with volumes runs

**A new volume starts empty**, and it hides whatever the image has at `mountPath`. Most apps create their data directory's contents on first start. If yours expects files the image ships at that path, mount the volume somewhere else.

**Your app can write to it whichever user it runs as.** Pond gives a new volume to group `1000`, and adds that group to the container's processes. Images whose entrypoint switches to another user still work; they usually fix the ownership of their data directory themselves.

## Sizes

A size can grow but never shrink. Pond rejects a size smaller than the last successful deployment to the same environment used, before anything is deployed. Sizes are compared by value, so `1024Mi` and `1Gi` are the same size.

Growing only works if the cluster's storage supports expanding volumes. Where it doesn't, as with the `local-path` storage of k3s and Rancher Desktop, a larger size fails the deploy. Put the old size back and deploy again: a failed deployment doesn't count towards the shrink check.

Some storage, `local-path` included, doesn't enforce the size at all. Treat it as the space you ask for, not as a quota.

## Removing a volume

Removing a volume from `pond.yml` stops mounting it, but **the data stays in the cluster**. Adding a volume with the same name back mounts the old data again. It keeps the size it had, so the cluster rejects a smaller size, even though Pond can't check it in that case.

Pond doesn't delete volume data. That will be an explicit action later; until then, it takes an operator.

## Config files inside a volume

A [config file](config-files.md) can be mounted inside a volume's directory, for example Home Assistant's `/config/configuration.yaml` in a `/config` volume:

```yaml
configs:
  configuration.yaml:
    format: yaml
    mountPath: /config/configuration.yaml
    values:
      default_config: {}

volumes:
  config:
    type: shared-single-node
    mountPath: /config
    size: 1Gi
```

The config file is mounted over the volume, read-only, and rendered again on every deploy. The rest of `/config` is the volume's.

The other way round isn't allowed: a volume can't be mounted at a config file's path, or below it.

## Volumes inside volumes

Volumes can be nested, for example `/var/www/html` and `/var/www/html/data`. This is useful when one part of the data is much larger than the rest.

## Upgrading Pond

**Upgrade the agents before the server.** An agent from before volumes doesn't create a volume's storage. The deploy then fails, with the pod stuck in `Pending` on `persistentvolumeclaim "<service>-<volume>" not found`, instead of running without its data.

**A service last deployed by an older agent needs one deploy before it can get a volume.** Any deploy without the volume is enough, such as a new image tag. If the first deploy after the upgrade adds the volume, it fails with:

```
spec.strategy.rollingUpdate: Forbidden: may not be specified when strategy `type` is 'Recreate'
```

Nothing is lost: the running copy keeps running. Deploy once without the volume, then add it.

## Known limitations

- **Storage and backups belong to the cluster.** Volumes use the cluster's default storage class, and Pond doesn't back them up. Choosing storage per environment is planned.
- **A first deploy with `replicas: 0` fails** on storage that only creates a volume once a pod uses it, as `local-path` does (`WaitForFirstConsumer`). The volume never becomes ready, and the rollout times out. Deploy with one replica first.
- **Two services can claim the same volume name.** Service `a` with volume `b-c` and service `a-b` with volume `c` both get the name `a-b-c`. The second deploy fails, rather than sharing the data.
- **A config file inside a volume leaves an empty file behind.** The container runtime creates an empty placeholder at the config's path inside the volume. It stays there if the config is removed later.

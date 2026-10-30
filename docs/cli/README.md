# CLI Reference

The `pond` CLI is the developer-facing interface to the Pond platform.

## Global flags

These flags apply to every command. Each value is resolved from the first source that
supplies it, in this order:

**flag → environment variable → active context → default**

| Flag | Env var | Description |
|---|---|---|
| `-s, --server` | `POND_SERVER_URL` | Server base URL (default `http://localhost:8080`) |
| `--token` | `POND_TOKEN` | Bearer token for authentication |
| `--project` | `POND_PROJECT`, then `POND_PROJECT_ID` | Project UUID |
| `-e, --env` | `POND_ENV` | Target environment name |
| `-c, --context` | `POND_CONTEXT` | Named context to use instead of the active context |
| `--kube-context` | `POND_KUBE_CONTEXT` | kubectl context for the port-forward |
| `--kube-namespace` | `POND_KUBE_NAMESPACE` | Kubernetes namespace for the port-forward |
| `--kube-service` | `POND_KUBE_SERVICE` | Kubernetes service for the port-forward (default `pond-server`) |
| `--kubectl-forward-port` | `POND_KUBECTL_FORWARD_PORT` | Local port the port-forward binds |

`POND_PROJECT` and `POND_PROJECT_ID` are interchangeable; `POND_PROJECT` wins when both
are set. `POND_KUBE_CONTEXT` is a *kubectl* context and is unrelated to `--context` /
`POND_CONTEXT`, which selects a named *pond* context.

## Configuration file

The CLI stores contexts in `~/.pond/config` (YAML). Use the `pond context` commands to manage it — do not edit the file manually.

`POND_CONFIG` overrides the path. It is a single path: unlike `KUBECONFIG` there is no
`:`-separated merge list and no `~` expansion, and a relative path resolves against the
current working directory. Pointing it at a path that does not exist gives an empty
configuration with no contexts, which is a reliable way to keep a script or CI job from
falling back to a developer's own context — the end-to-end suite does exactly that.

Server settings live at the top level of a context; port-forward settings are grouped
under `port-forward`. Every key has an environment variable equivalent, listed in the
table above:

```yaml
current-context: prod
contexts:
  prod:
    server: http://localhost:24680
    token: ey...
    project: 8f3c...
    env: staging
    port-forward:
      kube-context: my-cluster
      kube-namespace: pond
      kube-service: pond-server
      kubectl-forward-port: 24680
```

---

## pond context

Manage named contexts. A context bundles a server URL, token, project, and environment under a single name.

### pond context add \<name\>

```sh
pond context add <name> --server <url> [--token <tok>] [--project <uuid>] [--env <env>]
```

Creates or updates a context. `--server` is required. The context is written to
`~/.pond/config`, or to `POND_CONFIG` when that is set.

If the server is only reachable inside a Kubernetes cluster, add port-forward settings.
The CLI then starts a background `kubectl port-forward` whenever the forward port is not
already listening:

```sh
pond context add prod \
  --server               http://localhost:24680 \
  --kube-context         my-cluster \
  --kube-namespace       pond \
  --kube-service         pond-server \
  --kubectl-forward-port 24680
```

`--kube-context`, `--kube-namespace` and `--kubectl-forward-port` must be set together —
an incomplete combination is ignored with a warning. `--kube-service` defaults to
`pond-server`. The forward binds `--kubectl-forward-port` locally and targets port `8080`
on the service — point `--server` at that same local port. `scripts/homelab-quick-setup.sh`
picks a random free forward port for you.

The same four settings can come from flags or environment variables instead of a context,
so a port-forward can be configured with no config file at all. They are independent of
`--server`, though: overriding the forward port alone binds a new local port while the
server URL still points at the old one, so override `POND_SERVER_URL` alongside it.

### pond context use \<name\>

```sh
pond context use prod
```

Sets the active context. Subsequent commands use this context unless overridden with `--context`.

### pond context list

```sh
pond context list
```

Lists all contexts. The active context is marked with `*`.

### pond context show

```sh
pond context show
```

Prints the active context's details. The token is truncated.

This prints what is *stored* in the config file, not the configuration a command will
actually use: flags and environment variables override it, so the two can disagree.

### pond context current

```sh
pond context current
```

Prints the name of the active context.

### pond context remove \<name\>

```sh
pond context remove old-context
```

Removes a context from the config file.

---

## pond init

Create a `pond.yml` for a new service, and the input file `pond deploy` reads for the environment.

```sh
pond init --image <image> [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--image` | _(required)_ | Docker image reference, without tag |
| `--name` | the directory name | Service name. Without it, the name of the pond file's directory is lowercased, and every run of characters other than `a-z` and `0-9` becomes `-`, so `My_App` gives `my-app` |
| `--port` | `8080` | Container port the application listens on |
| `-f, --pond-file` | `./pond.yml` | Path of the `pond.yml` to create |

The `pond.yml` has the required fields and `service.port`, followed by commented-out `gateway`, `env` and `dependencies` blocks to start from. The input file, `pond-input-<env>.yml` next to the pond file, is all comments: it sends no input until you uncomment a dependency or variable, and its commented `db` matches the one in `pond.yml`. See the [pond.yml reference](../services/pond-yml.md) for every field.

The environment is resolved like any other command's: `--env`, then `POND_ENV`, then the active context. With none set, only `pond.yml` is created. `pond init` doesn't contact the server or start a port-forward.

It never overwrites: if the `pond.yml`, `pond-input-<env>.yml` or `pond-input-<env>.json` already exists, it fails without writing anything. The values are checked like a deployed `pond.yml`'s, so a name that isn't a valid service name, or a directory name longer than 53 characters, fails too.

```sh
$ cd my-app
$ pond init --image ghcr.io/acme/my-app --port 3000 --env production
Created pond.yml
Created pond-input-production.yml
Next: pond deploy --tag <tag>
```

---

## pond deploy

Submit a deployment request.

```sh
pond deploy [flags]
```

| Flag | Default | Description |
|---|---|---|
| `-f, --pond-file` | `./pond.yml` | Path to the service definition file |
| `-t, --tag` | _(required)_ | Image tag to deploy |
| `-i, --input` | `pond-input-<env>.yml` or `.json`, if present | Path to a JSON or YAML file with input for dependencies and variables; see [Dependencies](../services/dependencies.md) for the format |
| `--no-wait` | `false` | Exit once the deployment is submitted instead of waiting for it to finish |

The command waits for the deployment: it polls every 2 seconds and exits when the deployment succeeds, fails or is cancelled. A failed deployment exits non-zero, with the failure reason and the logs of the failed step. With `--no-wait`, it prints the deployment ID and exits; follow it with [`pond deployment status --follow`](#pond-deployment-status). `--wait`, which waiting used to need, is still accepted and changes nothing.

While waiting, it shows what is being deployed and which step it is on. A deployment has one step per managed dependency, then the rollout. Each step that finishes stays on screen with how long it took:

```
Deployment submitted: 3f2a… (status: pending)
Deploying api (ghcr.io/acme/api:v1.2.3) to prod
✓ Provisioned db (postgres)  42s
⠹ [2/2] Rolling out api · 12s · Executing command helm upgrade ...
```

On a terminal, the last line is redrawn in place. It shows the current step, how long it has been running and its latest log line. When stdout is not a terminal, `CI` is set or `TERM=dumb`, as in CI/CD pipelines, the output is plain lines without escape codes. There is one line each time the step changes, and a line every 30 seconds while it doesn't, with the time and the latest log line:

```
[1/2] Provisioning db (postgres): waiting for the agent
[1/2] Provisioning db (postgres)
[1/2] Provisioning db (postgres) · 30s · aws_db_instance.this: Still creating... [30s elapsed]
✓ Provisioned db (postgres)  42s
[2/2] Rolling out api
✓ Rolled out api  35s
Deployment succeeded in 1m28s
```

`waiting for the agent` means the agent of the environment's cluster hasn't picked the step up yet. It runs one command at a time, so it may be busy with another deployment. See [How work runs](../README.md#how-work-runs).

Only one deployment of a service runs in an environment at a time. While a deploy or delete of the service is in progress in the environment, the server refuses the deploy and creates nothing; the command exits non-zero, naming the deployment in progress. Wait for it to finish, or [cancel it](#pond-deployment-cancel), and deploy again. Deploying to other environments is not affected.

Every dependency declared in `pond.yml` needs input on every deployment; input from earlier deployments is not reused. If `--input` leaves out a declared dependency, or names a dependency that is not declared, the server rejects the request and creates nothing.

Without `--input`, the command looks for `pond-input-<env>.yml` or `pond-input-<env>.json` in the directory of the pond file, where `<env>` is the resolved environment, and uses it if present. If both exist, the command fails; remove one or pass `--input`. Files ending in `.yml` or `.yaml` are read as YAML, everything else as JSON.

**Examples:**

```sh
pond deploy --tag v1.2.3                                   # uses ./pond-input-<env>.yml or .json if present
pond deploy --tag v1.2.3 --input ./deploy-input.json
pond deploy --tag v1.2.3 --no-wait                         # exits once the deployment is submitted
```

---

## pond deployment

Parent command for inspecting and interacting with deployments.

### pond deployment status

```sh
pond deployment status [service] [flags]
```

Shows the latest deployment of a service. The service is looked up by name inside the resolved project, so `--project` (or `POND_PROJECT` / `POND_PROJECT_ID`, or a context) is required.

When the service argument is omitted, the name is read from the `name` field of `./pond.yml` (or of `--pond-file`), so the command can be run without arguments from a service directory. A service argument always wins over `pond.yml`.

With no environment set, the command prints one row per environment of the project — the latest deployment there, or `-` when the service has never been deployed to it:

```sh
$ pond deployment status api
Service api

ENVIRONMENT  STATUS     IMAGE   DEPLOYMENT                            CREATED
dev          -          -       -                                     -
prod         succeeded  v1.2.3  55555555-5555-5555-5555-555555555555  2026-09-12 10:30:00
```

When an environment is set — via `--env`, `POND_ENV` or the active context — the command narrows to that environment and prints the full state of its latest deployment: status, image tag, triggered-by, timestamps, dependency list, and command logs for any failed or queued commands.

```sh
pond deployment status api --env prod
```

With `--follow`, the command streams the logs of every command of that one deployment as they arrive, along with each command's status changes, until the deployment finishes. It then exits non-zero if the deployment failed or was cancelled. Following needs a single deployment, so it requires an environment or `--deployment-id`. Following a deployment that has already finished prints all of its logs and exits.

```sh
$ pond deployment status api --env prod --follow
...
Command 66666666-6666-6666-6666-666666666666 (deploy): dispatched
  pulling image
  installing
Command 66666666-6666-6666-6666-666666666666 (deploy): succeeded

Deployment succeeded
```

| Flag | Required | Description |
|---|---|---|
| `-e, --env` | no | Restrict to one environment and show its full deployment detail |
| `-f, --pond-file` | no | Path to the `pond.yml` to read the service name from (default `./pond.yml`) |
| `--deployment-id` | no | Show one deployment by UUID instead of a service; cannot be combined with a service name |
| `--follow` | no | Stream the deployment's logs until it finishes; needs an environment or `--deployment-id` |

### pond deployment list

```sh
pond deployment list [service] [flags]
```

Lists the deployment history of a service, newest first. The service is looked up by name inside the resolved project, and can be given either as the positional argument or with `--service` — passing both is an error. When neither is given, the name is read from the `name` field of `./pond.yml` (or of `--pond-file`), so the command can be run without arguments from a service directory.

```sh
pond deployment list --service api
```

With no environment set, deployments from every environment are listed together, each row naming its environment:

```sh
$ pond deployment list api
Service api

DEPLOYMENT                            ENVIRONMENT  STATUS     IMAGE       TRIGGERED  CREATED              COMPLETED
4eeb2f2b-9124-4cd7-978a-fb2cbbb02555  dev          running    v1.3.0-rc1  cli        2026-09-14 09:00:00  -
55555555-5555-5555-5555-555555555555  prod         succeeded  v1.2.3      cli        2026-09-12 10:30:00  2026-09-12 10:31:12
66666666-6666-6666-6666-666666666666  prod         failed     v1.0.0      ci         2026-09-10 08:00:00  2026-09-10 08:15:00
```

When an environment is set — via `--env`, `POND_ENV` or the active context — only that environment's deployments are listed, and the environment column is dropped in favour of the header:

```sh
$ pond deployment list api --env prod
Service api (environment: prod)

DEPLOYMENT                            STATUS     IMAGE   TRIGGERED  CREATED              COMPLETED
55555555-5555-5555-5555-555555555555  succeeded  v1.2.3  cli        2026-09-12 10:30:00  2026-09-12 10:31:12
66666666-6666-6666-6666-666666666666  failed     v1.0.0  ci         2026-09-10 08:00:00  2026-09-10 08:15:00
```

A `-` in the completed column means the deployment has not finished yet. Use `pond deployment status --deployment-id <id>` to inspect any row in full.

| Flag | Required | Description |
|---|---|---|
| `--service` | no | Service name, as an alternative to the positional argument; cannot be combined with it |
| `-e, --env` | no | Restrict the listing to one environment |
| `-f, --pond-file` | no | Path to the `pond.yml` to read the service name from (default `./pond.yml`) |
| `--limit` | no | Maximum number of deployments to list (default 20) |

### pond deployment cancel

```sh
pond deployment cancel [service] [flags]
```

Cancels the deployment of a service in progress in an environment, or a delete started by `pond service delete`. Steps that have not started are cancelled at once. The agent is told to stop the step it runs, and until it has, the deployment shows as `cancelling`, then `cancelled`. If the agent has lost its connection to the server, it is told when it connects again; see [How work runs](../README.md#how-work-runs).

The service is looked up by name inside the resolved project, as by `pond deployment status`. When the service argument is omitted, the name is read from the `name` field of `./pond.yml` (or of `--pond-file`). An environment is required, via `--env`, `POND_ENV` or the active context: a service has at most one deployment in progress per environment, and that one is cancelled.

Before cancelling, the command shows that deployment and the step it is on, and asks for a `y`. Any other answer, or none, cancels nothing. `--yes` skips the question; without a terminal, as in CI, pass it or pipe `y` in.

```sh
$ pond deployment cancel api --env prod
This cancels the deployment of service api to prod:
  Deployment: 4eeb2f2b-9124-4cd7-978a-fb2cbbb02555
  Status:     running
  Step:       [1/2] Provisioning db (postgres)
  Image:      v1.3.0
  Triggered:  cli
  Created:    2026-09-14 09:00:00
Cancel it? [y/N]: y
Deployment 4eeb2f2b-9124-4cd7-978a-fb2cbbb02555 cancelled
The agent is told to stop provisioning db (postgres); the deployment shows as cancelling until it has.
```

With `--deployment-id`, that deployment is cancelled without asking, and no service or environment is needed.

When nothing is in progress — the latest deployment in the environment, or the one given, has already finished or been cancelled — the command cancels nothing and exits non-zero.

A cancelled deployment doesn't block the next one: you can deploy again right away, and its steps run once the agent has stopped. What was done before the cancel is not undone: dependencies already provisioned stay, and a cancelled rollout or delete leaves the service where it stopped. Deploy again, or run `pond service delete` again, to finish the job.

| Flag | Required | Description |
|---|---|---|
| `-e, --env` | yes, unless `--deployment-id` is given | Environment of the deployment to cancel |
| `-f, --pond-file` | no | Path to the `pond.yml` to read the service name from (default `./pond.yml`) |
| `--deployment-id` | no | Cancel one deployment by UUID, without asking; cannot be combined with a service name |
| `-y, --yes` | no | Cancel without asking for confirmation |


---

## pond service

Parent command for managing services.

### pond service list

```sh
pond service list [flags]
```

Lists the services of the resolved project, sorted by name, so `--project` (or `POND_PROJECT` / `POND_PROJECT_ID`, or a context) is required.

With no environment set, each row has the service's status: `deleting` while a delete of it runs in some environment, `deleted` once it is deleted from every environment, and `active` otherwise, including when a delete of it failed somewhere. A deleted service is still listed; deploying it again makes it `active`:

```sh
$ pond service list
NAME    STATUS   CREATED
api     active   2026-09-01 12:00:00
legacy  deleted  2026-08-20 14:05:00
worker  active   2026-09-03 16:45:00
```

When an environment is set — via `--env`, `POND_ENV` or the active context — each row has the service's latest deployment in that environment instead, as in `pond deployment status`, or `-` when the service has never been deployed there:

```sh
$ pond service list --env prod
Environment prod

NAME    STATUS     IMAGE   DEPLOYMENT                            CREATED
api     succeeded  v1.2.3  55555555-5555-5555-5555-555555555555  2026-09-12 10:30:00
legacy  deleted    v0.9.0  9a1c07d2-5b3e-4f6a-8d21-6c0e5f4b7a39  2026-09-11 09:00:00
worker  -          -       -                                     -
```

| Flag | Required | Description |
|---|---|---|
| `-e, --env` | no | Show each service's latest deployment in this environment |

### pond service delete

```sh
pond service delete [service] [flags]
```

Deletes a service from every environment it is deployed to. In each one, its helm release is uninstalled first, so no pod of the service is left running, and then its [managed dependencies](../services/dependencies.md#managed-vs-manual-dependencies) are destroyed, with their data. Volumes are kept.

The service is looked up by name inside the resolved project, as by `pond deployment status`. When the service argument is omitted, the name is read from the `name` field of `./pond.yml` (or of `--pond-file`).

There is no environment to choose: the service goes from all of them. `--env` and `POND_ENV` are refused, so that a script that means one environment doesn't delete from all of them unnoticed. An environment from the active context is ignored, with a note on stderr.

Before deleting, the command lists the environments and asks for the service name to be typed back. Any other answer, or none, deletes nothing. `--yes` skips the question; without a terminal, as in CI, pass it or pipe the name in.

```sh
$ pond service delete api
This deletes service api from prod, staging.
Its managed dependencies, such as databases, are destroyed with it: their data is lost.
Volumes are kept.
Type the service name to confirm: api
Delete submitted: 4eeb2f2b-9124-4cd7-978a-fb2cbbb02555 from prod (pending)
Delete submitted: 9a1c07d2-5b3e-4f6a-8d21-6c0e5f4b7a39 from staging (pending)
Volumes are kept: deploying api again mounts their data again
```

Without `--wait`, the command exits once the deletes are submitted. With `--wait`, it follows each environment's delete in turn, the way `pond deploy` follows a deployment, and exits non-zero if any of them failed or was cancelled.

The server refuses the delete, and nothing changes, when:

- **Something is in progress.** A deployment or delete of the service is in progress in some environment. Wait for it, or [cancel it](#pond-deployment-cancel), and delete again.
- **Another service depends on it.** A service with a [`service` dependency](../services/dependencies.md#service) on this one is deployed to one of its environments. Delete that one first, or remove the dependency from it and redeploy it.

While a delete runs, deploying the service is refused.

A delete that fails in one environment doesn't undo the others. Running `pond service delete` again retries only the environments the service is still deployed to; a release that is already gone and a dependency already destroyed are skipped.

A deleted service is still listed: `pond service list` and `pond deployment status` show it as `deleted`, or `deleting` while a delete runs, and each environment's latest deployment as `deleted`, `deleting` or `delete failed`. Deploying it again under the same name brings it back, with its history.

| Flag | Required | Description |
|---|---|---|
| `-f, --pond-file` | no | Path to the `pond.yml` to read the service name from (default `./pond.yml`) |
| `--keep-deps` | no | Keep the managed dependencies: only uninstall the service. Their tofu state stays, so deploying the service again under the same name reattaches them |
| `-y, --yes` | no | Delete without asking for confirmation |
| `--wait` | no | Wait until the service is deleted from every environment |

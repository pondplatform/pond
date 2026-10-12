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
on the service — point `--server` at that same local port. `scripts/setup.sh` picks a
random free forward port for you.

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

## pond deploy

Submit a deployment request.

```sh
pond deploy [flags]
```

| Flag | Default | Description |
|---|---|---|
| `-f, --pond-file` | `./pond.yml` | Path to the service definition file |
| `-t, --tag` | _(required)_ | Image tag to deploy |
| `-i, --input` | `pond-input-<env>.yml` or `.json`, if present | Path to a JSON or YAML file with input for dependencies; see [Dependencies](../services/dependencies.md) for the format |
| `--wait` | `false` | Block until the deployment reaches a terminal state |

Without `--wait`, the command prints the deployment ID and exits. With `--wait`, it polls every 2 seconds and exits when the deployment succeeds, fails or is cancelled.

Every dependency declared in `pond.yml` needs input on every deployment; input from earlier deployments is not reused. If `--input` leaves out a declared dependency, or names a dependency that is not declared, the server rejects the request and creates nothing.

Without `--input`, the command looks for `pond-input-<env>.yml` or `pond-input-<env>.json` in the directory of the pond file, where `<env>` is the resolved environment, and uses it if present. If both exist, the command fails; remove one or pass `--input`. Files ending in `.yml` or `.yaml` are read as YAML, everything else as JSON.

**Examples:**

```sh
pond deploy --tag v1.2.3 --wait                                   # uses ./pond-input-<env>.yml or .json if present
pond deploy --tag v1.2.3 --input ./deploy-input.json --wait
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


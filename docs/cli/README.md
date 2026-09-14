# CLI Reference

The `pond` CLI is the developer-facing interface to the Pond platform.

## Global flags

These flags apply to every command. They can also be set via environment variables or a named context.

| Flag | Env var | Description |
|---|---|---|
| `-s, --server` | `POND_SERVER_URL` | Server base URL (default `http://localhost:8080`) |
| `--token` | `POND_TOKEN` | Bearer token for authentication |
| `--project` | `POND_PROJECT` | Project UUID |
| `-e, --env` | `POND_ENV` | Target environment name |
| `--context` | — | Named context to use instead of the active context |

## Configuration file

The CLI stores contexts in `~/.pond/config` (YAML). Use the `pond context` commands to manage it — do not edit the file manually.

---

## pond context

Manage named contexts. A context bundles a server URL, token, project, and environment under a single name.

### pond context add \<name\>

```sh
pond context add <name> --server <url> [--token <tok>] [--project <uuid>] [--env <env>]
```

Creates or updates a context. `--server` is required.

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
| `-c, --config` | `./pond.yml` | Path to the service definition file |
| `-t, --tag` | _(required)_ | Image tag to deploy |
| `--wait` | `false` | Block until the deployment reaches a terminal state |

Without `--wait`, the command prints the deployment ID and exits. With `--wait`, it polls every 2 seconds and exits when the deployment succeeds or fails. If the server is waiting for dependency input (`awaiting_input` state), it prints a hint to run `pond deployment configure` and exits.

**Example:**

```sh
pond deploy --tag v1.2.3 --wait
```

---

## pond deployment

Parent command for inspecting and interacting with deployments.

### pond deployment status

```sh
pond deployment status [service] [flags]
```

Shows the latest deployment of a service. The service is looked up by name inside the resolved project, so `--project` (or `POND_PROJECT`, or a context) is required.

When the service argument is omitted, the name is read from the `name` field of `./pond.yml` (or of `--config`), so the command can be run without arguments from a service directory. A service argument always wins over `pond.yml`.

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

| Flag | Required | Description |
|---|---|---|
| `-e, --env` | no | Restrict to one environment and show its full deployment detail |
| `-c, --config` | no | Path to the `pond.yml` to read the service name from (default `./pond.yml`) |
| `--deployment-id` | no | Show one deployment by UUID instead of a service; cannot be combined with a service name |

### pond deployment configure

```sh
pond deployment configure --deployment-id <id> --file <path>
```

Provides dependency input for a deployment that is in the `awaiting_input` state. The `--file` argument points to a JSON file that specifies how each dependency should be resolved.

| Flag | Required | Description |
|---|---|---|
| `--deployment-id` | yes | Deployment UUID |
| `-f, --file` | yes | Path to a JSON file with dependency configuration |

**Example input file:**

```json
{
  "dependencies": {
    "db": {
      "managed": true,
      "values": { "instances": 1 }
    }
  }
}
```

See [Dependencies](../services/dependencies.md) for the full format.

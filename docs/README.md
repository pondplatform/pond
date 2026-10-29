# Pond

Pond is a deployment platform that lets you deploy containerised applications to Kubernetes clusters without writing Helm charts or managing infrastructure by hand.

You describe your service in a single `pond.yml` file, run `pond deploy --tag <image-tag>`, and Pond handles the rest — provisioning dependencies (e.g. a managed PostgreSQL database), rendering configuration, and driving Helm upgrades on the target cluster.

## Architecture

Pond has three components that work together:

```
CLI (pond)
    │  REST API  (JWT bearer token)
    ▼
┌─────────────────────────────────────────────┐
│  pond-server                                │
│  REST API · Deployment state machine        │
│  PostgreSQL                                │
└──────────────────┬──────────────────────────┘
                   │  WebSocket (per cluster)
                   ▼
            pond-agent
            (runs inside your Kubernetes cluster)
            Helm · OpenTofu
```

**pond-server** — The control plane. Accepts deployment requests from the CLI, orchestrates dependency provisioning, and dispatches Helm/OpenTofu commands to agents via WebSocket.

**pond-agent** — Runs inside each Kubernetes cluster. Receives commands from the server and executes `helm upgrade --install` and `tofu apply` using a service account with cluster-admin permissions.

**pond CLI** — The developer-facing tool. Submits deployments, checks status, and supplies dependency configuration when required.

### How work runs

- **Each cluster runs exactly one agent.** The `pond-agent` chart runs one replica, and on upgrade stops the old pod before it starts the new one. The agent keeps OpenTofu state on its own volume.
- **The agent runs one command at a time.** It asks for a command, runs it, reports the result, then asks again. Commands run in the order they were queued, across every deployment to the cluster. A deployment can wait while the agent works on another.
- **A command doesn't survive its agent.** If the agent restarts or loses its connection while running a command, the command's outcome is unknown. When the agent connects again, the server fails the command with `the agent disconnected while running this command, so its outcome is unknown`, which fails its deployment. Deploy again.
- **Don't run a second agent for a cluster.** When it connects, the server fails whatever the first one is running, and the two then run commands side by side.

## Navigation

- [Quick Start](getting-started/quick-start.md) — install the CLI and deploy your first service
- [pond.yml Reference](services/pond-yml.md) — full service format reference
- [CLI Reference](cli/README.md) — all commands and flags
- [Operators](operators/installation.md) — install and run Pond in a Kubernetes cluster

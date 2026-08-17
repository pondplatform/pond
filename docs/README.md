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
│  PostgreSQL · RabbitMQ                      │
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

## Navigation

- [Quick Start](getting-started/quick-start.md) — install the CLI and deploy your first service
- [pond.yml Reference](services/pond-yml.md) — full service format reference
- [CLI Reference](cli/README.md) — all commands and flags
- [Operators](operators/installation.md) — install and run Pond in a Kubernetes cluster

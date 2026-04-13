# Pond

Pond is a cloud-agnostic deployment orchestration platform. It coordinates Kubernetes service deployments with their infrastructure dependencies (managed via OpenTofu/Terraform), sequencing IaC provisioning before Helm upgrades in a single pipeline.

## Architecture

Three binaries communicate through a central server:

```
CLI (pond)
  └─ HTTP REST ──► Server (pond-server)
                       ├─ PostgreSQL (queue, state, config)
                       └─ WebSocket ──► Agent (pond-agent, runs in-cluster)
                                            ├─ helm upgrade --install
                                            └─ tofu init/apply/output
```

**CLI** (`cmd/cli`, `internal/cli/`) — Cobra-based. Parses `pond.yml`, resolves environment overrides, and submits deployments via HTTP.

**Server** (`cmd/server`, `internal/server/`) — REST API + WebSocket gateway. Orchestrates deployments by enqueuing commands to a PostgreSQL-backed queue. Agents poll and execute commands asynchronously; results drive a state machine forward.

**Agent** (`cmd/agent`, `internal/agent/`) — Deployed once per cluster. Connects to server over WebSocket with a bearer token. Executes `helm` and `tofu` subprocesses, streams logs, and returns results.

## Deployment Flow

1. CLI POSTs to `/deployments` with a `ServiceConfig` snapshot and image tag.
2. Server creates a `deployment` record (status `pending`) and enqueues `tofu.apply` commands for each *managed* dependency.
3. Agent runs `tofu apply` per dependency, returns outputs over WebSocket.
4. Once all tofu commands succeed, server merges outputs into `resolved_contexts`, generates Helm values, and enqueues `helm.upgrade`.
5. Agent runs `helm upgrade --install`, returns result.
6. Server marks deployment `succeeded` or `failed`.

Deployments with no managed dependencies skip straight to step 4.

## Key Packages

| Path | Purpose |
|------|---------|
| `internal/common/domain/` | Core domain types and repository interfaces — imports nothing |
| `internal/common/config/` | `pond.yml` parsing and environment override resolution |
| `internal/server/api/` | HTTP handlers and router |
| `internal/server/service/` | Business logic and orchestration (deployment state machine, helmgen, dependency resolution) |
| `internal/server/store/` | Repository implementations (one per entity) |
| `internal/server/queue/` | PostgreSQL-backed command queue |
| `internal/agent/` | WebSocket client, command dispatcher, helm/tofu runners |
| `internal/cli/` | CLI commands and HTTP client |

## Server Architecture

The server follows a **layered architecture**. Dependencies flow downward only:

```
api  →  service  →  store / queue  →  domain
```

**Rules:**
1. Each layer imports only the layer directly below it — no skipping layers
2. Handlers never call stores directly
3. Services depend on repository interfaces, never on concrete store types
4. `api/` owns HTTP concerns only: routing, request parsing, input validation, response serialization
5. Business logic lives exclusively in `service/`
6. `store/` defines and implements repository interfaces — both live together in `store/`
7. `store/` and `queue/` handle infrastructure only — no business logic
8. `internal/common/domain/` contains only data: structs, enums, error sentinels — no interfaces or behavioral contracts
9. `internal/common/` contains only what is shared across binaries: domain types and config parsing. Helm value generation and dependency resolution are server concerns and live under `internal/server/service/`

## Database

Schema lives in `db/schema.sql`. Directly modify that file in case of DB changes. Notable tables:

- `deployments` — immutable snapshot of `ServiceConfig` at deploy time (JSONB)
- `command_queue` — FIFO queue consumed by agents (rows deleted on dequeue)
- `command_results` — command outcomes keyed by command ID
- `dependency_deployment_requests` — tracks each `tofu.apply` per deployment
- `dependency_configs` — per-service/environment config: managed flag, provider inputs, user config
- `resolved_contexts` — cached, merged dependency outputs ready for Helm injection

Agent tokens are stored as SHA-256 hashes; plaintext tokens are never persisted.

## Configuration: `pond.yml`

Services are described in `pond.yml` with a base config and per-environment overrides. The CLI resolves these into a flat `ServiceConfig` before sending to the server. See `internal/common/config/` for parsing and merge logic.

## Running Locally

```sh
make build          # build all three binaries
make server         # start server (needs DATABASE_URL)
make agent          # start agent (needs POND_SERVER_ADDR, POND_AGENT_TOKEN)
make cli            # build CLI binary
make migrate        # run DB migrations
make test           # run tests
make e2e            # run end-to-end tests
```

Environment variables:
- **Server:** `DATABASE_URL`, `LISTEN_ADDR`
- **Agent:** `POND_SERVER_ADDR`, `POND_AGENT_TOKEN`
- **CLI:** `POND_SERVER_URL` (default `localhost:8080`)

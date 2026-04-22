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

## Deployment State Machine

The deployment orchestration is event-driven, implemented in `internal/server/service/deployment.go`. The `DeploymentService.Start()` method subscribes to events and drives transitions.

### Dependency Resolution Flow

Each dependency declared in `pond.yml` goes through resolution before helm can run. The flow depends on whether the dependency was previously configured (`scheduleDependency` in `dependency.go`):

```
                                    ┌─────────────────┐
                                    │ New dependency  │
                                    │ (no prev config)│
                                    └────────┬────────┘
                                             │
                                             ▼
                                    ┌─────────────────┐
                                    │ awaiting_input  │◄── User must provide:
                                    │                 │    • managed (bool)
                                    └────────┬────────┘    • provider_inputs
                                             │
                          ┌──────────────────┴──────────────────┐
                          │                                     │
                          ▼                                     ▼
               ┌─────────────────┐                   ┌─────────────────┐
               │ managed = true  │                   │ managed = false │
               │                 │                   │ (manual dep)    │
               └────────┬────────┘                   └────────┬────────┘
                        │                                     │
                        ▼                                     │
               ┌─────────────────┐                            │
               │ pending         │                            │
               │ + tofu.apply    │                            │
               │   command       │                            │
               └────────┬────────┘                            │
                        │                                     │
                        ▼                                     │
               ┌─────────────────┐                            │
               │ Agent executes  │                            │
               │ tofu apply      │                            │
               └────────┬────────┘                            │
                        │                                     │
           ┌────────────┴────────────┐                        │
           ▼                         ▼                        │
    ┌────────────┐           ┌────────────┐                   │
    │ succeeded  │           │  failed    │                   │
    │ (outputs   │           │            │                   │
    │  stored)   │           └─────┬──────┘                   │
    └─────┬──────┘                 │                          │
          │                        ▼                          │
          │               ┌─────────────────┐                 │
          │               │ Cancel siblings │                 │
          │               │ Deployment fails│                 │
          │               └─────────────────┘                 │
          │                                                   │
          └───────────────────────┬───────────────────────────┘
                                  │
                                  ▼
                         ┌─────────────────┐
                         │ All deps done?  │
                         └────────┬────────┘
                                  │ yes
                                  ▼
                         ┌─────────────────┐
                         │ Enqueue         │
                         │ helm.upgrade    │
                         └─────────────────┘
```

**Three dependency paths:**

1. **First-time dependency** — No previous config exists. Status set to `awaiting_input`. Deployment blocks until user provides `managed` flag and `provider_inputs` via API. Publishes `UserInputRequired` event.

2. **Managed dependency** — Previous config has `managed=true`. Enqueues `tofu.apply` command with vars from `provider_inputs`. Agent runs tofu, outputs stored on success.

3. **Manual (non-managed) dependency** — Previous config has `managed=false`. No tofu command. Uses `user_config` directly as outputs. Immediately considered complete.

### Batch Input Collection

When a deployment has dependencies requiring user input, the system waits for **all** inputs before scheduling **any** commands:

```
Deployment submitted
        │
        ▼
┌─────────────────────┐
│ Create all dep      │
│ configs             │
└─────────┬───────────┘
          │
          ▼
┌─────────────────────┐
│ Any awaiting input? │
└─────────┬───────────┘
          │
    yes   │   no
    ┌─────┴─────┐
    │           │
    ▼           ▼
Publish      Dispatch all
UserInput    CommandQueued
Required     events
events
    │
    ▼
Wait for ALL inputs
    │
    ▼
All inputs provided?
    │ yes
    ▼
Schedule ALL deps at once
(dispatch all CommandQueued)
```

This ensures deployments with multiple first-time dependencies don't partially execute while waiting for remaining inputs. The `handleUserInputProvided` handler in `deployment.go` checks `AnyDepConfigAwaitingInput()` after each input is provided — only when all return false does it call `ScheduleAfterInput()` for every dependency and dispatch their commands together.

### Deployment Status Transitions

```
pending → running → succeeded
                  ↘ failed
```

- **pending** — created, waiting for agent to pick up first command
- **running** — agent acknowledged command start
- **succeeded** — helm upgrade completed successfully
- **failed** — any tofu or helm command failed

### Event Handlers

| Event | Handler | Effect |
|-------|---------|--------|
| `AgentReady` | `handleAgentReady` | Dispatches next queued command for cluster |
| `CommandStarted` | `handleCommandStarted` | Deployment pending → running |
| `CommandResult` | `processResult` → `advance` | Routes to `advanceDependency` or `advanceHelm` |
| `AgentDisconnected` | `handleAgentDisconnected` | Requeues in-flight command |

All state transitions run in database transactions to prevent races between concurrent dependency completions.

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
make test           # run tests
make test-integration # run integration tests
```

Environment variables:
- **Server:** `DATABASE_URL`, `LISTEN_ADDR`
- **Agent:** `POND_SERVER_ADDR`, `POND_AGENT_TOKEN`
- **CLI:** `POND_SERVER_URL` (default `localhost:8080`)

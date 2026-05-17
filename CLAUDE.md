# Pond

Pond is a cloud-agnostic deployment orchestration platform. It coordinates Kubernetes service deployments with their infrastructure dependencies (managed via OpenTofu/Terraform), sequencing IaC provisioning before Helm upgrades in a single pipeline.

## Architecture

Three binaries communicate through a central server:

```
CLI (pond)
  └─ HTTP REST ──► Server (pond-server)
                       ├─ PostgreSQL (state, config)
                       ├─ RabbitMQ (event bus)
                       └─ WebSocket ──► Agent (pond-agent, runs in-cluster)
                                            ├─ helm upgrade --install
                                            └─ tofu init/apply/output
```

**CLI** (`cli/`) — Cobra-based. Parses `pond.yml`, resolves environment overrides, and submits deployments via HTTP.

**Server** (`server/`) — REST API + WebSocket gateway. Orchestrates deployments via a RabbitMQ-backed event bus. Agents connect over WebSocket, receive commands, and return results that drive a state machine forward.

**Agent** (`agent/`) — Deployed once per cluster. Connects to server over WebSocket with a cluster bearer token. Executes `helm` and `tofu` subprocesses, streams logs, and returns results.

## Go Workspace Layout

The project is a **Go workspace** (`go.work`) with four independent modules:

```
/
├── shared/     # module: github.com/pondplatform/pond/shared
├── cli/        # module: github.com/pondplatform/pond/cli
├── server/     # module: github.com/pondplatform/pond/server
├── agent/      # module: github.com/pondplatform/pond/agent
├── infra/      # Dockerfiles, Helm charts, Tofu modules (not Go code)
├── test/       # E2E scripts and test-data apps
└── docker-compose.yml
```

Each of `cli/`, `server/`, and `agent/` declares `replace github.com/pondplatform/pond/shared => ../shared` in their `go.mod`.

## Key Packages

### `shared/` — Cross-binary types (no binary)

| Package | Purpose |
|---------|---------|
| `shared/serviceconfig` | `ServiceConfig`, `OverridableConfig`, `DependencyDeclaration`, and all `pond.yml` schema types |
| `shared/serviceconfig/config` | `ConfigParser` (parse `pond.yml`) + `ConfigResolver` (apply per-env overrides into flat `ServiceConfig`) |
| `shared/server/api` | HTTP request/response types for every API resource; `ValidationError`, `ErrNotFound`, `ErrInvalidInput` |
| `shared/agent/wire` | WebSocket wire protocol: `Envelope`, `CommandPayload`, `AckPayload`, `ResultPayload`, `LogPayload`; command type constants (`helm.upgrade`, `tofu.apply`, `tofu.output`) |

### `server/` — REST API + orchestration engine

| Package | Purpose |
|---------|---------|
| `server/internal/api` | HTTP handlers and router — owns HTTP concerns only (routing, parsing, validation, serialization) |
| `server/internal/service` | Business logic: `DeploymentService`, `DependencyService`, `AgentConnectionService`, deployment state machine (`deployment_advance.go`) |
| `server/internal/store` | Repository interfaces + SQL implementations for every entity |
| `server/internal/model/db` | Domain model structs: `Deployment`, `Command`, `CommandLog`, `DependencyDeployment`, `Service`, `Environment`, `Organization`, `Cluster` |
| `server/internal/events` | `Bus` interface + `RabbitMQBus`: `SubscribeWork` (exactly-once), `SubscribeFanout` (broadcast), `Publish` |
| `server/internal/helmgen` | `HelmValuesGenerator`: `ServiceConfig` + dep outputs → Helm values YAML |
| `server/internal/dependency` | `SpecRegistry` with built-in specs (`postgres`, `http-service`); `DependencyService` implementation |
| `server/internal/auth` | `Authenticator` (admin-key → JWT chain), `Authorizer` (role-based), `SHA256Hex` |
| `server/internal/transactor.go` | `Transactor` wrapping `*sql.DB` for service-layer transactions |
| `server/db/migrations` | `golang-migrate` SQL migration files (embedded in binary, run on startup) |
| `server/internal/integration` | Integration test harness using testcontainers (real Postgres + RabbitMQ) |

### `agent/` — Cluster-side daemon

| Package | Purpose |
|---------|---------|
| `agent/internal/agent` | `Run()` loop, `Connection`, `Executor`; `HelmRunner`/`TofuRunner` interfaces |
| `agent/internal/agent/helm` | `helm.NewRunner()`: wraps `helm upgrade --install` subprocess, streams logs |
| `agent/internal/agent/tofu` | `tofu.NewRunner()`: wraps `tofu init` + `tofu apply` + `tofu output` subprocess calls |

### `cli/` — User-facing CLI

| Package | Purpose |
|---------|---------|
| `cli/internal/cli` | `NewRootCmd()` wiring all subcommands |
| `cli/internal/cli/client` | `ServerClient` interface + `NewHTTPClient` / `NewHTTPClientWithToken` |
| `cli/internal/cli/commands` | Individual cobra commands (`deploy`, `deployment configure`, etc.) |

## Server Architecture

The server follows a **layered architecture**. Dependencies flow downward only:

```
api  →  service  →  store  →  model/db
              ↓
           events
```

**Rules:**
1. Handlers (`api/`) own HTTP concerns only — never call stores directly
2. Business logic lives exclusively in `service/`
3. Services depend on repository interfaces defined in `store/`, never concrete types
4. `store/` handles SQL only — no business logic
5. `model/db` contains only structs and enums — no interfaces or behavior
6. `shared/` contains only what is shared across binaries: `ServiceConfig` schema, HTTP types, WebSocket wire types
7. Helm value generation (`helmgen/`) and dependency spec registry (`dependency/`) are server concerns — they do not belong in `shared/`

## Deployment Flow

1. CLI POSTs to `/api/v1/deployments` with a `ServiceConfig` snapshot and image tag.
2. Server creates a `deployment` record and dependency records for each declared dependency.
3. If any dependency has no prior config → status `awaiting_input`; user must provide `managed` flag + `provider_inputs` via API.
4. Once all inputs are available: managed dependencies enqueue `tofu.apply` commands; non-managed dependencies are immediately considered complete.
5. Agent executes `tofu apply` per dependency, returns outputs over WebSocket.
6. Once all dependency commands succeed, server generates Helm values and enqueues `helm.upgrade`.
7. Agent runs `helm upgrade --install`, returns result.
8. Server marks deployment `succeeded` or `failed`.

Deployments with no managed dependencies skip directly to step 6.

## Deployment State Machine

The orchestration is event-driven via RabbitMQ, implemented in `server/internal/service/deployment.go`.

### Dependency Resolution

```
New dependency (no prior config)
         │
         ▼
  awaiting_input ◄── User provides: managed (bool) + provider_inputs
         │
    ┌────┴────┐
    │         │
managed=true  managed=false
    │         │
    ▼         │
 pending      │
 + tofu.apply │
   command    │
    │         │
    ▼         │
 Agent runs   │
 tofu apply   │
    │         │
 ┌──┴──┐      │
 ▼     ▼      │
succ  fail    │
       │      │
       ▼      │
  Cancel siblings
  Deployment fails
       │      │
succ ──┴──────┘
       │
All deps done?
       │ yes
       ▼
Enqueue helm.upgrade
```

**Three dependency paths:**

1. **First-time** — No prior config. Status `awaiting_input`. Blocks until user provides `managed` + `provider_inputs`.
2. **Managed** — Prior config has `managed=true`. Enqueues `tofu.apply`; agent runs tofu, outputs stored on success.
3. **Non-managed** — Prior config has `managed=false`. No tofu command; `user_config` used directly as outputs. Immediately complete.

### Batch Input Collection

When multiple dependencies need user input, the system waits for **all** inputs before scheduling **any** commands. The `handleUserInputProvided` handler checks `AnyDepConfigAwaitingInput()` after each input — only when all return false does it call `ScheduleAfterInput()` and dispatch all commands together.

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
| `CommandStarted` | `handleCommandStarted` | Deployment `pending` → `running` |
| `CommandResult` | `processResult` → `advance` | Routes to `advanceDependency` or `advanceHelm` |
| `AgentDisconnected` | `handleAgentDisconnected` | Requeues in-flight command |

All state transitions run in database transactions to prevent races between concurrent dependency completions.

## HTTP API

All routes require authentication (JWT bearer or admin key), except the agent WebSocket.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/deployments` | Submit a deployment |
| `GET` | `/api/v1/deployments/{id}` | Get deployment status |
| `POST` | `/api/v1/deployments/{id}/user-input` | Provide dependency configuration |
| `POST` | `/api/v1/deployments/{id}/cancel` | Cancel deployment |
| `GET` | `/api/v1/commands/{commandId}/logs` | Stream command logs |
| `GET` | `/api/v1/services/{serviceId}/deployments` | List deployments for a service |
| `POST/GET` | `/api/v1/organizations` | Create / list orgs |
| `GET` | `/api/v1/organizations/{id}` | Get org |
| `POST/GET` | `/api/v1/organizations/{orgId}/clusters` | Create / list clusters |
| `GET` | `/api/v1/organizations/{orgId}/clusters/{id}` | Get cluster |
| `POST` | `/api/v1/organizations/{orgId}/clusters/{id}/rotate-token` | Rotate cluster token |
| `POST` | `/api/v1/organizations/{orgId}/tokens` | Create API token (admin only) |
| `POST/GET` | `/api/v1/organizations/{orgId}/projects` | Create / list projects |
| `GET/PATCH` | `/api/v1/projects/{id}` | Get / update project |
| `POST/GET` | `/api/v1/projects/{projectId}/environments` | Create / list environments |
| `GET/PATCH` | `/api/v1/environments/{id}` | Get / update environment |
| `GET` | `/api/v1/projects/{projectId}/services` | List services |
| `GET` | `/api/v1/services/{id}` | Get service |
| `GET` | `/api/v1/dependency-specs` | List built-in dependency specs |
| `GET` | `/api/v1/dependency-specs/{type}` | Get spec by type |
| `GET` | `/agent/ws` | Agent WebSocket (cluster-token auth) |

## Database

Migrations live in `server/db/migrations/` as embedded SQL files, run automatically via `golang-migrate` on server startup. Notable tables:

- `organizations` — top-level tenant
- `clusters` — one per Kubernetes cluster; stores `agent_token_hash` (SHA-256, plaintext never persisted)
- `projects` — groups services; has a `root_environment_id`
- `environments` — inherits from parent; holds `namespace`, `cluster_id`, `default_ingress_base_host`
- `services` — tracks `current_deployment_id` and `running_deployment_id`
- `deployments` — `service_config_snapshot` (JSONB), `status`, `helm_command_id`
- `dependency_deployments` — per-dependency state: `managed`, `provider_inputs`, `user_config`, `output` (all JSONB); status: `awaiting_input | pending | succeeded | failed`
- `commands` — command queue rows: `type`, `payload` (JSONB), `status`: `queued | running | succeeded | failed | cancelled`
- `command_logs` — append-only log lines per command

## Configuration: `pond.yml`

Services are described in `pond.yml` with a base config and per-environment overrides. The CLI resolves these via `shared/serviceconfig/config` into a flat `ServiceConfig` before POSTing to the server.

```yaml
version: 1
name: my-service
image: registry/my-service:latest

service:
  port: 3000
  replicas: 2

ingress:
  enabled: true

management:
  health:
    endpoint: /healthz
  metrics:
    endpoint: /metrics
    port: 9090

env:
  KEY: value

dependencies:
  db:
    type: postgres
    config:
      version: "15"

configs:
  .env:
    format: env
    mountDir: /app/
    values:
      DB_HOST: "{{db.host}}"  # references dep output

overrides:
  production:
    service:
      replicas: 3
```

## Running Locally

```sh
make build            # build all modules
make build-cli        # build CLI binary to ./bin/pond
make test             # run unit tests across all modules
make test-integration # run integration tests (testcontainers)
```

Start dependencies:
```sh
docker-compose up -d  # starts postgres:17 + rabbitmq:4 + pond-server
```

## Environment Variables

**`pond-server`**

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://localhost:5432/pond?sslmode=disable` | PostgreSQL DSN |
| `LISTEN_ADDR` | `:8080` | HTTP listen address |
| `RABBITMQ_URL` | `amqp://guest:guest@localhost:5672/` | RabbitMQ AMQP URL |
| `POND_JWT_SECRET` | (required) | JWT signing secret — server refuses to start without it |
| `POND_ADMIN_KEY` | (none) | Static master key granting unrestricted API access |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

**`pond-agent`**

| Variable | Default | Description |
|----------|---------|-------------|
| `POND_SERVER_ADDR` | `localhost:8080` | Server address for WebSocket connection |
| `POND_AGENT_TOKEN` | (none) | Cluster bearer token |
| `LOG_LEVEL` | `info` | Log level |

**`pond` CLI**

| Variable | Default | Description |
|----------|---------|-------------|
| `POND_SERVER_URL` | `http://localhost:8080` | Server base URL |
| `POND_TOKEN` | (none) | Bearer token for API authentication |

## Infra Assets

| Path | Purpose |
|------|---------|
| `infra/docker/` | Dockerfiles for all three binaries |
| `infra/deploy/helm/pond-server/` | Helm chart deploying server + postgres + rabbitmq |
| `infra/deploy/helm/pond-agent/` | Helm chart deploying agent with ClusterRole |
| `infra/agent/helm-charts/base-service/` | Helm chart the agent uses to deploy user services |
| `infra/agent/tofu-providers/postgres/` | OpenTofu module for provisioning a PostgreSQL database in-cluster |

The agent Docker image embeds both `helm-charts/` and `tofu-providers/` at build time (copied to `/opt/pond/`).

## Building Code for Claude

After doing large changes, always verify with `make test-integration`
# Pond — Product Specification

> Version: 0.1 (pre-production)
> Status: Architecture planning

---

## 1. Overview

Pond is a SaaS deployment platform for managing application deployments into clients' Kubernetes clusters. It provides a structured model for defining services, their infrastructure dependencies, and environment-specific configuration, and automates the full deployment lifecycle including dependency provisioning, secret management, config generation, and Helm chart rollout.

The system is designed around three components:

| Component | Location | Responsibility |
|-----------|----------|----------------|
| **CLI** | Developer machine | Submit deployment requests, interact with server |
| **Server** | Pond infrastructure | Store state, orchestrate deployments, host dashboard |
| **Agent** | Client K8s cluster | Execute deployments on behalf of the server |

---

## 2. System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Developer machine                                              │
│                                                                 │
│   ┌──────────┐                                                  │
│   │ pond CLI │                                                  │
│   └────┬─────┘                                                  │
└────────┼────────────────────────────────────────────────────────┘
         │ HTTPS (REST/gRPC)
         ▼
┌─────────────────────────────────────────────────────────────────┐
│  Pond infrastructure                                            │
│                                                                 │
│   ┌──────────────────────────────────────────────────────────┐  │
│   │  Pond Server                                             │  │
│   │                                                          │  │
│   │  - Deployment state & history                            │  │
│   │  - Secret/credential storage                             │  │
│   │  - Provider config storage                               │  │
│   │  - Environment & project registry                        │  │
│   │  - Dashboard / UI                                         │  │
│   │  - Deployment queue                                       │  │
│   └──────────────────────────┬───────────────────────────────┘  │
└──────────────────────────────┼──────────────────────────────────┘
                               │ Persistent connection (WebSocket / gRPC stream)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  Client Kubernetes cluster                                      │
│                                                                 │
│   ┌──────────────────────────────────────────────────────────┐  │
│   │  Pond Agent (workload)                                   │  │
│   │                                                          │  │
│   │  - Connects to server, waits for commands                │  │
│   │  - Deploys Helm charts                                   │  │
│   │  - Runs Terraform/OpenTofu                               │  │
│   │  - Reports status back to server                         │  │
│   └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│   ┌──────────────────────────────────────────────────────────┐  │
│   │  Client workloads (deployed services)                    │  │
│   └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### 2.1 CLI

The CLI is a thin client. It no longer runs deployments locally. Its responsibilities are:

- Authenticate with the Pond server
- Submit deployment requests for a project / environment
- Poll or stream deployment status (with optional `--wait` flag)
- Manage local configuration (default project, auth tokens)
- Surface developer utilities (`pond run`, `pond psql`, `pond port-forward`)

### 2.2 Server

The server is the central control plane. It owns all persistent state:

- **Project and environment registry**: hierarchy, environment config
- **Service configurations**: current and historical `pond.yml` contents
- **Dependency configurations**: managed vs. manual, provider inputs
- **Secrets and credentials**: encrypted at rest, scoped per project/environment
- **Deployment queue**: requests, status, logs, audit trail
- **Dashboard**: web UI for viewing deployments, pending user input, environment health

The server calls the agent to perform actual cluster operations. It does not have direct access to client clusters.

### 2.3 Agent

The agent runs as a Kubernetes workload inside the client cluster. It is intentionally simple:

- Opens a persistent connection to the Pond server
- Receives deployment commands (Helm install/upgrade, Terraform apply, etc.)
- Executes them against the local cluster
- Streams logs and status back to the server

The agent has no persistent state. All state lives in the server.

---

## 3. Data Model

### 3.1 Hierarchy

```
Organization
└── Project (e.g., "acme-backend")
    ├── Services
    │   ├── Service: "api"
    │   ├── Service: "worker"
    │   └── Service: "frontend"
    └── Environment tree
        ├── pre  (root — deploys here first by default)
        │   └── pro
        └── ...
```

A **Project** groups one or more services with a shared set of environments. **Services** are independent deployable units within a project — each with its own `pond.yml`, deployment history, and per-environment dependency state. **Environments** form a directed tree: deployments can propagate from parent to child environments.

When `pond deploy` is run without an explicit `--env`, the request targets the root environment of the project's tree.

**Project**

| Field | Type | Description |
|-------|------|-------------|
| `id` | uuid | |
| `name` | string | Unique per organization, slug-safe |
| `organization_id` | uuid | |
| `root_environment_id` | uuid | First node in the environment tree |
| `created_at` | timestamp | |

**Environment**

| Field | Type | Description |
|-------|------|-------------|
| `id` | uuid | |
| `name` | string | e.g. `pre`, `pro`, `staging` |
| `project_id` | uuid | |
| `parent_environment_id` | uuid? | null for root |
| `namespace` | string | Kubernetes namespace |
| `default_ingress_base_host` | string | e.g. `staging.example.com` |
| `cluster_id` | uuid | Which agent/cluster to target |

**Service**

A service is a distinct deployable unit belonging to a project. It is registered when first deployed or explicitly created. The server tracks the current live state (which image tag is running, which dependency context was used) per environment.

| Field | Type | Description |
|-------|------|-------------|
| `id` | uuid | |
| `name` | string | Unique within a project, matches `name` in `pond.yml` |
| `project_id` | uuid | |
| `created_at` | timestamp | |

**Deployment**

Each deployment is scoped to a service + environment pair. The server stores a full snapshot of the effective config (base + overrides applied) so the exact state that produced a deployment is always reproducible.

| Field | Type | Description |
|-------|------|-------------|
| `id` | uuid | |
| `service_id` | uuid | |
| `environment_id` | uuid | |
| `service_config_snapshot` | json | Effective `pond.yml` at deploy time (overrides already applied) |
| `image_tag` | string | |
| `status` | enum | `pending`, `running`, `succeeded`, `failed`, `awaiting_input` |
| `triggered_by` | string | CLI user / automated |
| `created_at` | timestamp | |
| `completed_at` | timestamp? | |

### 3.2 Dependency Model

Dependencies represent infrastructure or external services that a workload requires at runtime. They are declared in `pond.yml` and tracked per service and environment on the server.

Each named dependency in a service has two mutable server-side records, both keyed by `(service_id, environment_id, dependency_name)`:

#### DependencyConfig

Stores how the dependency is wired up for a specific service in a specific environment. Set once by an operator or developer via `pond configure` or the dashboard; does not change between deployments unless explicitly updated.

| Field | Type | Description |
|-------|------|-------------|
| `id` | uuid | |
| `service_id` | uuid | |
| `environment_id` | uuid | |
| `dependency_name` | string | Matches key in `pond.yml` `dependencies` map |
| `dependency_type` | string | e.g. `postgres`, `kafka` |
| `managed` | bool | Whether Pond provisions this resource |
| `provider_inputs` | json | Inputs for the managed provider (operator-supplied) |
| `user_config` | json | Manual connection values (developer-supplied, non-managed) |
| `updated_at` | timestamp | |

#### DependencyResolvedContext

Stores the last-known resolved output for a dependency in a given environment. For managed dependencies this is populated after each successful provider run; for manual dependencies it mirrors `user_config`. This is the data injected into config templates at deploy time.

| Field | Type | Description |
|-------|------|-------------|
| `id` | uuid | |
| `service_id` | uuid | |
| `environment_id` | uuid | |
| `dependency_name` | string | |
| `context` | json (encrypted) | Resolved key/value map (e.g. `host`, `port`, `password`) |
| `resolved_at` | timestamp | When the provider last produced this output |
| `source_deployment_id` | uuid? | Deployment that triggered this resolution |

**Built-in dependency types** define the expected schema of the resolved context:

| Type | Resolved fields |
|------|----------------|
| `postgres` | `host`, `port`, `username`, `password`, `database` |
| `kafka` | `brokers`, `username`, `password` (planned) |
| `secret` | arbitrary key/value (planned) |

#### Dependency config spaces summary

| Space | Record | Owner | Mutability |
|-------|--------|-------|------------|
| **Provider inputs** | `DependencyConfig.provider_inputs` | Operator / admin | Set once per environment, updated manually |
| **User config** | `DependencyConfig.user_config` | Developer | Set once per environment, updated manually |
| **Resolved context** | `DependencyResolvedContext.context` | Provider (runtime) | Written by server after each provider run |

### 3.3 Provider Model

A **managed provider** takes `provider_inputs` and produces the **resolved context**. Currently implemented: OpenTofu (Terraform) backed provider for PostgreSQL.

```
DependencyConfig.provider_inputs
        │
        ▼
ManagedProvider.Apply()   (executed by agent)
        │
        ▼
DependencyResolvedContext.context   (stored encrypted on server)
        │
        ▼
Config template rendering at deploy time
```

Providers are executed by the agent. The agent receives the provider inputs and the provider implementation reference (e.g., a Terraform module), runs the apply, and returns the outputs to the server. The server stores them in `DependencyResolvedContext` and uses them for all subsequent deployments until the provider is re-run.

---

## 4. Service Configuration (`pond.yml`)

The `pond.yml` file lives at the root of a service's repository. It is the source of truth for how a service is deployed.

### 4.1 Schema

```yaml
version: 1

# Service identity
name: my-service                     # Unique within a project
image: ghcr.io/myorg/my-service      # Image repo (tag supplied at deploy time)
build: ./Dockerfile                  # Optional: build image locally before deploying

# Kubernetes service settings
service:
  port: 8080                         # Container port (default: 8080)
  replicas: 2                        # Pod replica count (default: 1)

# Ingress
ingress:
  enabled: true                      # Expose via ingress controller

# Observability
management:
  metrics:
    port: 9090
    endpoint: /metrics
  health:
    port: 8080
    endpoint: /healthz               # Used for liveness + readiness probes

# Infrastructure dependencies
dependencies:
  db:                                # Dependency name (used in template vars)
    type: postgres
    config:                          # Provider-type-specific options (provider inputs)
      version: "15"

  legacy-api:
    type: http-service               # Non-managed external service
    config:
      url: https://api.legacy.example.com

# Static environment variables
env:
  LOG_LEVEL: INFO
  FEATURE_X: "true"

# Configuration files mounted into the container
configs:
  application.yml:
    format: yaml                     # yaml | json | env
    mountDir: /app/config
    values:
      server:
        port: "{{service.port}}"
      database:
        host: "{{db.host}}"
        port: "{{db.port}}"
        username: "{{db.username}}"
        password: "{{db.password}}"
        name: "{{db.database}}"

# Per-environment overrides (merged on top of base config)
overrides:
  pre:
    service:
      replicas: 1
    env:
      LOG_LEVEL: DEBUG
  pro:
    service:
      replicas: 3
    ingress:
      enabled: true
```

### 4.2 Config Template Variables

Config file `values` support `{{variable}}` interpolation. Variables are resolved from dependency context at deploy time.

| Syntax | Source |
|--------|--------|
| `{{depName.field}}` | Resolved dependency context field |
| `{{service.port}}` | Service config field |
| `{{env.ENV_VAR}}` | Static env variable |

All variables must resolve before deployment proceeds; unresolved variables are a fatal error.

### 4.3 Environment Overrides

Overrides are merged on top of the base config for the target environment. The merge strategy is:

- **Scalars** (replicas, port, enabled): override replaces base value
- **Maps** (env, dependencies): deep merge, override keys win
- **Lists**: not currently supported in overrides

---

## 5. CLI Specification

### 5.1 Global Flags

```
--env <name>        Target environment (defaults to project root environment)
--project <name>    Target project (defaults to inferred from pond.yml name or .pond/config)
--wait              Wait for deployment to complete before returning
--no-wait           Return immediately after submitting (default)
--output json|text  Output format (default: text)
```

### 5.2 Commands

#### `pond deploy`

Submit a deployment request to the server.

```
pond deploy [flags]

Flags:
  -c, --config string     Path to pond.yml (default: ./pond.yml)
  -t, --tag string        Image tag to deploy (required)
  -e, --env string        Target environment (default: project root)
      --wait              Wait for completion and stream logs
      --build             Build and push image before deploying
```

**Flow:**
1. Read `pond.yml`
2. Apply overrides for target environment
3. POST deployment request to server (includes config snapshot + image tag)
4. If `--wait`: stream deployment logs until terminal state
5. Exit 0 on success, non-zero on failure

#### `pond configure`

Set dependency configuration for an environment (provider inputs or manual user config).

```
pond configure [--env <env>] [--dependency <name>]

Interactive prompts guide the user through:
  - Is this dependency managed by Pond? (yes/no)
  - If managed: provider-specific inputs (e.g., instance count)
  - If manual: required connection values (host, port, credentials)
```

Configuration is stored on the server and associated with the environment.

#### `pond run`

Run the service locally, with dependencies proxied from an environment.

```
pond run [flags]

Flags:
  --proxy-dependencies-from-env <env>   Forward dependency connections from env
  --dependencies-only                   Only start dependency proxies, not the service
```

#### `pond psql`

Open a psql session against a PostgreSQL dependency in an environment.

```
pond psql [--env <env>] [<dependency-name>] [--service <service>]
```

#### `pond port-forward`

Forward a port from a running service or dependency in an environment.

```
pond port-forward <local-port>[:<remote-port>] [--env <env>] [--dependency <dep>]
```

#### `pond config generate`

Generate resolved config files for local inspection or use.

```
pond config generate [<config-name>] [--all] [--output-dir <dir>] [--env <env>]
```

#### `pond dashboard`

Open the Pond web dashboard in the browser.

```
pond dashboard
```

#### `pond proxy-env`

Proxy all dependency connections from a remote environment to localhost (convenience wrapper around port-forward).

```
pond proxy-env [--env <env>]
```

---

## 6. Server API (Internal)

The server exposes an API consumed by the CLI and the agent. Key resource groups:

### CLI → Server

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/deployments` | Submit a deployment request |
| `GET` | `/deployments/:id` | Get deployment status |
| `GET` | `/deployments/:id/logs` | Stream deployment logs (SSE or WebSocket) |
| `GET` | `/projects/:id/environments` | List environments |
| `GET` | `/projects/:id/services` | List services in a project |
| `GET` | `/services/:id/deployments` | List deployment history for a service |
| `PUT` | `/services/:id/environments/:env_id/dependencies/:name` | Set dependency config (provider inputs or user config) |
| `GET` | `/services/:id/environments/:env_id/dependencies/:name` | Get dependency config |
| `GET` | `/services/:id/environments/:env_id/dependencies/:name/context` | Get last resolved context for a dependency |

### Server → Agent

The server pushes commands to the agent over a persistent connection. Command types:

| Command | Payload |
|---------|---------|
| `helm.upgrade` | release name, chart, generated values |
| `tofu.apply` | module path, var inputs, state reference |
| `tofu.output` | module path, state reference |
| `status.query` | resource selector |

The agent responds with streaming log output and a terminal result event.

---

## 7. Agent Specification

The agent is a minimal workload deployed into the client's cluster.

### Requirements

- Runs as a Kubernetes Deployment (single replica)
- Requires cluster-level RBAC to deploy to target namespaces
- Outbound HTTPS to Pond server (no inbound)
- Has Helm CLI and OpenTofu CLI available (bundled in image)

### Connection Model

- Initiates a persistent connection to the Pond server on startup
- Re-connects on disconnect with exponential backoff
- Identified by a pre-issued `agent_token` (stored as a K8s Secret)

### Execution Model

- Receives commands one at a time from the server queue
- Executes in a sandboxed subprocess
- Streams stdout/stderr back line by line
- Reports final exit code to server

---

## 8. Secret & Credential Management

| Secret type | Storage | Encryption | Access |
|-------------|---------|------------|--------|
| Provider outputs (DB credentials, etc.) | Server database | Encrypted at rest (envelope encryption) | Agent receives per-deployment, never stored in cluster |
| Dependency user config (manual credentials) | Server database | Encrypted at rest | Same as above |
| Agent token | K8s Secret in client cluster | K8s-native | Agent only |
| User auth tokens (CLI) | `~/.pond/credentials` | OS keychain (planned) | CLI only |

**Principle:** secrets never leave the server in plaintext. The agent receives them as environment variables or temp files scoped to the deployment subprocess lifetime.

---

## 9. Deployment Flow (Future Architecture)

```
Developer runs: pond deploy --tag v1.2.3 --wait

CLI
 └─ Read pond.yml
 └─ POST /deployments  {config_snapshot, image_tag, env}
     │
     ▼
Server
 └─ Resolve service_id from config name + project
 └─ Create Deployment record (service_id, environment_id, status: pending)
 └─ Return deployment ID to CLI
 └─ For each dependency in config snapshot:
     ├─ Load DependencyConfig (service, env, dep name)
     ├─ Managed: load DependencyResolvedContext (last known outputs)
     │   └─ If stale or missing: queue provider run first
     └─ Manual: use DependencyConfig.user_config as resolved context
 └─ Generate Helm values
     ├─ Render config templates using resolved contexts
     └─ Build HelmValues struct
 └─ Push command to agent queue: helm.upgrade(...)
     │
     ▼
Agent (in client cluster)
 └─ Receive helm.upgrade command
 └─ If dependencies have managed providers:
     └─ Execute tofu.apply (provider) → send outputs to server
 └─ helm upgrade --install with generated values
 └─ Stream logs to server
 └─ Report success / failure
     │
     ▼
Server
 └─ Update Deployment status
 └─ Store deployment outputs (ingress URL, etc.)
 └─ If --wait: stream log events back to CLI
     │
     ▼
CLI
 └─ Display logs (if --wait)
 └─ Exit 0 on success
```

---

## 10. Environment Tree & Promotion

Environments are arranged in a tree per project. A deployment request specifies a target environment. If no environment is specified, it defaults to the root.

```
Project: acme-backend
  Environment tree:
    pre  (root)
    └── pro
```

**Promotion behavior** (planned, not MVP):

- A deployment to `pre` may automatically trigger a deployment to `pro` if configured.
- Promotions can require explicit approval (gate).
- The server tracks which image tag is live in each environment.

**Manual targeting:**

```bash
pond deploy --tag v1.2.3 --env pro   # Deploy directly to pro
pond deploy --tag v1.2.3             # Deploy to root (pre)
```

---

## 11. Provider Configuration vs. User Input

This section illustrates the three config spaces from section 3.2 with concrete examples.

### Provider Inputs (operator/admin concern)

Stored in `DependencyConfig.provider_inputs`, scoped to `(service, environment, dependency)`. Set once by an operator; drives the managed provider. Example: sizing a PostgreSQL instance in production.

```
Service: api
Environment: pro
Dependency: db (type: postgres, managed: true)

DependencyConfig.provider_inputs:
  instances: 2
  region: eu-west-1
  tier: db.t3.medium
```

These values are passed to the agent when it runs the provider (e.g., OpenTofu). They are operator-controlled and do not appear in `pond.yml`.

### User Config (developer concern)

Stored in `DependencyConfig.user_config`. Used when the dependency is not managed by Pond — the developer supplies connection values directly.

```
Service: api
Environment: pro
Dependency: legacy-api (type: http-service, managed: false)

DependencyConfig.user_config:
  url: https://api.legacy.example.com
  api_key: <secret>
```

### Resolved Context (runtime output)

Stored in `DependencyResolvedContext.context` after a successful provider run (managed) or copied from `user_config` (manual). This is what gets injected into config templates at deploy time.

```
Service: api
Environment: pro
Dependency: db

DependencyResolvedContext.context:
  host: prod-db.internal
  port: 5432
  username: svc_acme
  password: <encrypted>
  database: acme_pro
```

The `pond.yml` config template `{{db.host}}` resolves to `prod-db.internal` at deploy time by reading this record.

---

## 12. Helm Chart Integration

Pond uses a base Helm chart (`base-service`) as the deployment target for all services. Generated Helm values are passed at deploy time.

### Base Chart Capabilities

- Kubernetes Deployment, Service, ServiceAccount
- Ingress / HTTPRoute (gateway API)
- HorizontalPodAutoscaler
- Liveness and readiness probes (from `management.health`)
- Config file volumes (from `configs`, base64-encoded in generated values)
- Environment variable injection

### Generated Values Shape

```yaml
replicaCount: 2
image:
  repository: ghcr.io/myorg/my-service
  tag: v1.2.3
service:
  port: 8080
ingress:
  enabled: true
  host: my-service.example.com
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
env:
  LOG_LEVEL: INFO
configs:
  - name: application.yml
    mountDir: /app/config
    content: <base64-encoded rendered yaml>
```

---

## 13. Open Questions / Decisions Pending

1. **Agent auth model**: single agent token per cluster, or per-project token? How is rotation handled?
2. **Provider execution location**: does the agent always run providers, or can the server run them for managed infrastructure outside the client cluster?
3. **Environment promotion gates**: what constitutes a gate? Manual approval only, or automated checks (e.g., smoke tests passing)?
4. **Multi-cluster support**: can a single project deploy to environments in different clusters?
5. **Dashboard auth**: standalone auth or SSO integration?
6. **State storage for Terraform**: currently local `/tmp` — needs to move to server-side state backend (S3 / GCS bucket managed by Pond).
7. **Helm chart versioning**: base chart currently lives in the CLI repo — should it be hosted as a chart repository?
8. **CLI auth**: API key, OAuth device flow, or mutual TLS?

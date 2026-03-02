# Pond — Go Implementation Specification

> This is the canonical source of truth for the Go codebase architecture, package layout, interfaces, structs, and coding patterns.

---

## 1. Architecture Overview

Pond is a three-component system: **CLI**, **Server**, and **Agent**. All three are Go binaries living in a single monorepo. They share domain types via internal packages but are otherwise independent.

```
pond deploy --tag v1.2.3
       │
       ▼
┌─────────────┐     HTTPS/gRPC      ┌──────────────┐
│   CLI       │ ──────────────────▶  │   Server     │
│ (thin client)│                     │ (control plane)│
└─────────────┘                      └──────┬───────┘
                                            │ gRPC stream
                                            ▼
                                     ┌──────────────┐
                                     │   Agent      │
                                     │ (in-cluster) │
                                     └──────────────┘
```

### Design Principles

1. **Interface-driven design** — all cross-boundary dependencies are expressed as interfaces. Concrete implementations are injected at composition root (`main` or wire function).
2. **Dependency injection** — no package-level singletons, no `init()` side effects. Every service/repository receives its dependencies via constructor parameters.
3. **Testability first** — every interface can be replaced with a mock/fake in tests. Business logic is tested without databases, HTTP servers, or Kubernetes clusters.
4. **Domain-centric packaging** — packages are organized by domain concept (deployment, dependency, environment), not by technical layer (controllers, models, repositories).
5. **Explicit error handling** — errors are wrapped with context using `fmt.Errorf("...: %w", err)`. Domain-specific error types where callers need to branch on error kind.

---

## 2. Repository Layout

```
pond/
├── cmd/
│   ├── pond-cli/           # CLI entrypoint
│   │   └── main.go
│   ├── pond-server/        # Server entrypoint
│   │   └── main.go
│   └── pond-agent/         # Agent entrypoint
│       └── main.go
│
├── internal/
│   ├── domain/             # Pure domain types (no external deps)
│   │   ├── organization.go
│   │   ├── project.go
│   │   ├── environment.go
│   │   ├── cluster.go
│   │   ├── service.go
│   │   ├── deployment.go
│   │   ├── dependency.go
│   │   ├── serviceconfig.go
│   │   └── errors.go
│   │
│   ├── config/             # pond.yml parsing & override resolution
│   │   ├── parser.go       # Parse pond.yml into OverridableConfig
│   │   ├── resolver.go     # Apply overrides → ServiceConfig
│   │   ├── template.go     # {{var}} interpolation engine
│   │   └── interfaces.go
│   │
│   ├── dependency/         # Dependency management domain
│   │   ├── interfaces.go   # All dependency interfaces
│   │   ├── registry.go     # DependencyRegistry + ProviderRegistry impls
│   │   ├── context.go      # DependencyContextService impl
│   │   ├── specs.go        # Built-in dependency type specs (postgres, etc.)
│   │   └── provider/
│   │       └── tofu/
│   │           └── provider.go  # OpenTofu ManagedProvider impl
│   │
│   ├── deployment/         # Deployment orchestration domain
│   │   ├── interfaces.go
│   │   ├── service.go      # DeploymentService impl
│   │   └── validation.go   # Validation logic
│   │
│   ├── helmgen/            # Helm values generation
│   │   ├── interfaces.go
│   │   ├── generator.go    # ServiceConfig → HelmValues mapping
│   │   └── types.go        # HelmValues and sub-structs
│   │
│   ├── server/             # Server-specific code
│   │   ├── api/            # HTTP/gRPC handlers
│   │   │   ├── router.go
│   │   │   ├── deployment_handler.go
│   │   │   ├── dependency_handler.go
│   │   │   ├── environment_handler.go
│   │   │   ├── service_handler.go
│   │   │   └── middleware.go
│   │   ├── store/          # Database repositories (PostgreSQL)
│   │   │   ├── organization_store.go
│   │   │   ├── project_store.go
│   │   │   ├── environment_store.go
│   │   │   ├── cluster_store.go
│   │   │   ├── service_store.go
│   │   │   ├── deployment_store.go
│   │   │   └── dependency_store.go
│   │   ├── queue/          # Deployment command queue
│   │   │   └── queue.go
│   │   └── app.go          # Server composition root
│   │
│   ├── agent/              # Agent-specific code
│   │   ├── connection.go   # Server connection (gRPC stream client)
│   │   ├── executor.go     # Command execution dispatcher
│   │   ├── helm/
│   │   │   └── runner.go   # Helm CLI wrapper
│   │   ├── tofu/
│   │   │   └── runner.go   # OpenTofu CLI wrapper
│   │   └── app.go          # Agent composition root
│   │
│   └── cli/                # CLI-specific code
│       ├── commands/       # Cobra command definitions
│       │   ├── deploy.go
│       │   ├── configure.go
│       │   ├── run.go
│       │   ├── psql.go
│       │   ├── portforward.go
│       │   └── config_generate.go
│       ├── client/         # Server API client
│       │   └── client.go
│       └── app.go          # CLI composition root
│
├── docs/
│   ├── specification.md
│   ├── model.md
│   └── go-spec.md          # This file
│
├── go.mod
└── go.sum
```

### Key Conventions

- `internal/` — all packages are internal; nothing is exported outside the module
- `internal/domain/` — pure value types with no dependencies on infrastructure. Any package may import domain, but domain imports nothing else from the project
- `interfaces.go` — each domain package defines its interfaces in a dedicated file
- `cmd/*/main.go` — thin; constructs dependencies and calls `app.Run()`
- Store implementations live under `server/store/` because they are server-specific. The agent and CLI never touch the database directly

---

## 3. Domain Types (`internal/domain`)

These are plain structs with no behavior beyond basic validation. They represent the core entities of the system and are used across all three components.

### `organization.go`

```go
type Organization struct {
    ID        uuid.UUID
    Name      string
    CreatedAt time.Time
}
```

### `project.go`

```go
type Project struct {
    ID                uuid.UUID
    OrganizationID    uuid.UUID
    Name              string
    RootEnvironmentID *uuid.UUID // nil until first environment is created
    CreatedAt         time.Time
}
```

### `environment.go`

```go
type Environment struct {
    ID                    uuid.UUID
    ProjectID             uuid.UUID
    ParentEnvironmentID   *uuid.UUID // nil = root
    Name                  string
    Namespace             string
    DefaultIngressBaseHost string
    ClusterID             uuid.UUID
    CreatedAt             time.Time
}
```

### `cluster.go`

```go
type Cluster struct {
    ID               uuid.UUID
    OrganizationID   uuid.UUID
    Name             string
    AgentTokenHash   string
    LastSeenAt       *time.Time
    CreatedAt        time.Time
}
```

### `service.go`

```go
type Service struct {
    ID        uuid.UUID
    ProjectID uuid.UUID
    Name      string
    CreatedAt time.Time
}
```

### `deployment.go`

```go
type DeploymentStatus string

const (
    DeploymentStatusPending       DeploymentStatus = "pending"
    DeploymentStatusRunning       DeploymentStatus = "running"
    DeploymentStatusSucceeded     DeploymentStatus = "succeeded"
    DeploymentStatusFailed        DeploymentStatus = "failed"
    DeploymentStatusAwaitingInput DeploymentStatus = "awaiting_input"
)

type Deployment struct {
    ID                    uuid.UUID
    ServiceID             uuid.UUID
    EnvironmentID         uuid.UUID
    ImageTag              string
    ServiceConfigSnapshot ServiceConfig // effective config at deploy time
    Status                DeploymentStatus
    TriggeredBy           string
    CreatedAt             time.Time
    CompletedAt           *time.Time
}
```

### `dependency.go`

```go
// DependencyDeclaration represents a dependency as declared in pond.yml.
type DependencyDeclaration struct {
    Type   string
    Config map[string]any // developer-supplied config hints (e.g. postgres version)
}

// DependencyConfig represents the server-side wiring of a dependency
// for a specific (service, environment) pair.
type DependencyConfig struct {
    ID              uuid.UUID
    ServiceID       uuid.UUID
    EnvironmentID   uuid.UUID
    DependencyName  string
    DependencyType  string
    Managed         bool
    ProviderInputs  map[string]any // operator-supplied (managed)
    UserConfig      map[string]any // developer-supplied (non-managed)
    UpdatedAt       time.Time
}

// ResolvedContext holds the runtime-resolved values for a dependency,
// ready for injection into config templates.
type ResolvedContext struct {
    ID                 uuid.UUID
    ServiceID          uuid.UUID
    EnvironmentID      uuid.UUID
    DependencyName     string
    Values             map[string]any // e.g. {host, port, username, password, database}
    ResolvedAt         time.Time
    SourceDeploymentID *uuid.UUID
}

// DependencySpec describes a built-in dependency type's schema.
type DependencySpec struct {
    Type         string
    Description  string
    ConfigFields []FieldSpec // fields the developer sets in pond.yml
    OutputFields []FieldSpec // fields the resolved context must provide
}

type FieldSpec struct {
    Name        string
    Description string
    Required    bool
    Sensitive   bool
}
```

### `serviceconfig.go`

```go
// ServiceConfig is the fully-resolved configuration for deploying a service
// to a specific environment (overrides already applied).
type ServiceConfig struct {
    Version int    `json:"version" yaml:"version"`
    Name    string `json:"name"    yaml:"name"`
    Image   string `json:"image"   yaml:"image"`
    Build   string `json:"build"   yaml:"build"`

    Ingress  IngressConfig    `json:"ingress"  yaml:"ingress"`
    Service  ServiceSpec      `json:"service"  yaml:"service"`
    Manage   ManagementConfig `json:"management" yaml:"management"`

    Dependencies map[string]DependencyDeclaration `json:"dependencies" yaml:"dependencies"`
    Env          map[string]string                `json:"env"          yaml:"env"`
    Configs      map[string]ConfigFileSpec        `json:"configs"      yaml:"configs"`
}

type IngressConfig struct {
    Enabled bool `json:"enabled" yaml:"enabled"`
}

type ServiceSpec struct {
    Port     int32 `json:"port"     yaml:"port"`
    Replicas int32 `json:"replicas" yaml:"replicas"`
}

type ManagementConfig struct {
    Metrics MetricsConfig `json:"metrics" yaml:"metrics"`
    Health  HealthConfig  `json:"health"  yaml:"health"`
}

type MetricsConfig struct {
    Port     int    `json:"port"     yaml:"port"`
    Endpoint string `json:"endpoint" yaml:"endpoint"`
}

type HealthConfig struct {
    Port     int    `json:"port"     yaml:"port"`
    Endpoint string `json:"endpoint" yaml:"endpoint"`
}

type ConfigFileSpec struct {
    Format   string         `json:"format"   yaml:"format"`   // yaml | json | env
    MountDir string         `json:"mountDir" yaml:"mountDir"`
    Values   map[string]any `json:"values"   yaml:"values"`
}
```

### `errors.go`

```go
// Sentinel errors for domain-level error matching.
var (
    ErrNotFound      = errors.New("not found")
    ErrAlreadyExists = errors.New("already exists")
    ErrInvalidInput  = errors.New("invalid input")
)

// ValidationErrors aggregates multiple validation failures.
type ValidationErrors struct {
    Errors []ValidationError
}

type ValidationError struct {
    Component string // "config" | "environment" | "dependency"
    Field     string
    Message   string
}

type ValidationWarning struct {
    Component string
    Message   string
}

func (v *ValidationErrors) Error() string { ... }
func (v *ValidationErrors) Add(component, field, message string) { ... }
func (v *ValidationErrors) HasErrors() bool { ... }
```

---

## 4. Interfaces

Interfaces are the backbone of testability. Each package defines its interfaces in `interfaces.go`. Consumers depend on interfaces; implementations are injected.

### 4.1 Repository Interfaces (data access)

All repositories follow a consistent pattern: context-first, return `(*T, error)` or `([]T, error)`.

```go
// internal/domain — these interfaces are defined alongside domain types
// so that any package can depend on them without importing infrastructure.

type OrganizationRepository interface {
    GetByID(ctx context.Context, id uuid.UUID) (*Organization, error)
    GetByName(ctx context.Context, name string) (*Organization, error)
    Create(ctx context.Context, org *Organization) error
}

type ProjectRepository interface {
    GetByID(ctx context.Context, id uuid.UUID) (*Project, error)
    GetByName(ctx context.Context, orgID uuid.UUID, name string) (*Project, error)
    ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]Project, error)
    Create(ctx context.Context, project *Project) error
    SetRootEnvironment(ctx context.Context, projectID, envID uuid.UUID) error
}

type EnvironmentRepository interface {
    GetByID(ctx context.Context, id uuid.UUID) (*Environment, error)
    GetByName(ctx context.Context, projectID uuid.UUID, name string) (*Environment, error)
    ListByProject(ctx context.Context, projectID uuid.UUID) ([]Environment, error)
    Create(ctx context.Context, env *Environment) error
}

type ClusterRepository interface {
    GetByID(ctx context.Context, id uuid.UUID) (*Cluster, error)
    ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]Cluster, error)
    Create(ctx context.Context, cluster *Cluster) error
    UpdateLastSeen(ctx context.Context, id uuid.UUID, t time.Time) error
    GetByTokenHash(ctx context.Context, tokenHash string) (*Cluster, error)
}

type ServiceRepository interface {
    GetByID(ctx context.Context, id uuid.UUID) (*Service, error)
    GetByName(ctx context.Context, projectID uuid.UUID, name string) (*Service, error)
    ListByProject(ctx context.Context, projectID uuid.UUID) ([]Service, error)
    Create(ctx context.Context, svc *Service) error
}

type DeploymentRepository interface {
    GetByID(ctx context.Context, id uuid.UUID) (*Deployment, error)
    ListByService(ctx context.Context, serviceID uuid.UUID) ([]Deployment, error)
    Create(ctx context.Context, d *Deployment) error
    UpdateStatus(ctx context.Context, id uuid.UUID, status DeploymentStatus, completedAt *time.Time) error
}

type DependencyConfigRepository interface {
    Get(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*DependencyConfig, error)
    Set(ctx context.Context, cfg *DependencyConfig) error
    ListByServiceAndEnv(ctx context.Context, serviceID, envID uuid.UUID) ([]DependencyConfig, error)
}

type ResolvedContextRepository interface {
    Get(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*ResolvedContext, error)
    Set(ctx context.Context, rc *ResolvedContext) error
}
```

### 4.2 Service Interfaces (business logic)

```go
// internal/deployment/interfaces.go

type DeploymentService interface {
    // Submit creates a new deployment request. The server resolves dependencies,
    // generates Helm values, and queues a command for the agent.
    Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error)

    // GetStatus returns the current status of a deployment.
    GetStatus(ctx context.Context, deploymentID uuid.UUID) (*domain.Deployment, error)

    // Validate checks whether a deployment request would succeed without
    // actually executing it.
    Validate(ctx context.Context, req SubmitRequest) (*ValidationResult, error)
}

type SubmitRequest struct {
    ProjectID     uuid.UUID
    EnvironmentID uuid.UUID
    ServiceConfig domain.ServiceConfig
    ImageTag      string
    TriggeredBy   string
}

type ValidationResult struct {
    Valid    bool
    Errors   []domain.ValidationError
    Warnings []domain.ValidationWarning
}
```

```go
// internal/dependency/interfaces.go

// DependencyResolver coordinates resolving all dependencies for a service
// deployment in a given environment.
type DependencyResolver interface {
    // ResolveAll returns the resolved context for every dependency declared
    // in the service config, ready for config template interpolation.
    ResolveAll(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) (map[string]domain.ResolvedContext, error)

    // Validate checks that all dependencies are configured and resolvable.
    Validate(ctx context.Context, serviceID, envID uuid.UUID, deps map[string]domain.DependencyDeclaration) error
}

// ManagedProvider is implemented by each infrastructure provider (e.g. OpenTofu).
// Providers are executed by the agent.
type ManagedProvider interface {
    // InputFields describes which fields the operator must supply.
    InputFields() []domain.FieldSpec

    // Apply provisions the dependency and returns the resolved output values.
    Apply(ctx context.Context, req ProviderRequest) (map[string]any, error)

    // Destroy tears down a previously-provisioned dependency.
    Destroy(ctx context.Context, req ProviderRequest) error
}

type ProviderRequest struct {
    ServiceName    string
    DependencyName string
    DependencyType string
    Inputs         map[string]any
    Environment    domain.Environment
}

// SpecRegistry is a read-only registry of built-in dependency type schemas.
type SpecRegistry interface {
    Get(depType string) (domain.DependencySpec, error)
    List() []domain.DependencySpec
    Exists(depType string) bool
}

// ProviderRegistry maps dependency types to their ManagedProvider implementations.
type ProviderRegistry interface {
    Get(depType string) (ManagedProvider, error)
    Register(depType string, provider ManagedProvider) error
}
```

```go
// internal/config/interfaces.go

// ConfigParser reads and parses a pond.yml file.
type ConfigParser interface {
    Parse(r io.Reader) (*OverridableConfig, error)
    ParseFile(path string) (*OverridableConfig, error)
}

// ConfigResolver applies environment overrides to produce a final ServiceConfig.
type ConfigResolver interface {
    Resolve(base *OverridableConfig, envName string) (*domain.ServiceConfig, error)
}

// TemplateRenderer interpolates {{var}} placeholders in config file values
// using resolved dependency contexts.
type TemplateRenderer interface {
    Render(values map[string]any, contexts map[string]domain.ResolvedContext, svcConfig *domain.ServiceConfig) (map[string]any, error)
}
```

```go
// internal/helmgen/interfaces.go

// HelmValuesGenerator produces the Helm values YAML from a resolved
// service config and its dependency contexts.
type HelmValuesGenerator interface {
    Generate(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]domain.ResolvedContext) (*HelmValues, error)
}
```

### 4.3 Agent Interfaces

```go
// internal/agent/interfaces.go

// AgentConnection represents the persistent connection to the server.
type AgentConnection interface {
    Connect(ctx context.Context) error
    ReceiveCommand(ctx context.Context) (*Command, error)
    SendResult(ctx context.Context, result *CommandResult) error
    SendLog(ctx context.Context, entry LogEntry) error
    Close() error
}

// CommandExecutor dispatches and runs agent commands.
type CommandExecutor interface {
    Execute(ctx context.Context, cmd *Command) (*CommandResult, error)
}

// HelmRunner wraps Helm CLI operations.
type HelmRunner interface {
    Upgrade(ctx context.Context, req HelmUpgradeRequest) error
}

type HelmUpgradeRequest struct {
    ReleaseName string
    Namespace   string
    ChartPath   string
    Values      []byte // marshalled YAML
}

// TofuRunner wraps OpenTofu CLI operations.
type TofuRunner interface {
    Init(ctx context.Context, workDir string) error
    Apply(ctx context.Context, workDir string, vars map[string]string) error
    Output(ctx context.Context, workDir string) (map[string]any, error)
    Destroy(ctx context.Context, workDir string, vars map[string]string) error
}
```

### 4.4 CLI Interfaces

```go
// internal/cli/client/interfaces.go

// ServerClient is the CLI's interface to the Pond server API.
type ServerClient interface {
    SubmitDeployment(ctx context.Context, req SubmitDeploymentRequest) (*DeploymentResponse, error)
    GetDeployment(ctx context.Context, id uuid.UUID) (*DeploymentResponse, error)
    StreamDeploymentLogs(ctx context.Context, id uuid.UUID) (<-chan LogEntry, error)

    SetDependencyConfig(ctx context.Context, req SetDependencyConfigRequest) error
    GetDependencyConfig(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*DependencyConfigResponse, error)

    ListEnvironments(ctx context.Context, projectID uuid.UUID) ([]EnvironmentResponse, error)
    ListServices(ctx context.Context, projectID uuid.UUID) ([]ServiceResponse, error)
}
```

### 4.5 Server Queue Interface

```go
// internal/server/queue/interfaces.go

// CommandQueue manages the outbound command queue from server to agents.
type CommandQueue interface {
    Enqueue(ctx context.Context, clusterID uuid.UUID, cmd *Command) error
    Dequeue(ctx context.Context, clusterID uuid.UUID) (*Command, error)
    Acknowledge(ctx context.Context, cmdID uuid.UUID, result *CommandResult) error
}
```

---

## 5. Coding Patterns

### 5.1 Constructor Injection

Every service/repository struct receives its dependencies through the constructor. No package globals.

```go
// internal/deployment/service.go
type deploymentService struct {
    deployments domain.DeploymentRepository
    services    domain.ServiceRepository
    envs        domain.EnvironmentRepository
    resolver    dependency.DependencyResolver
    helmGen     helmgen.HelmValuesGenerator
    queue       queue.CommandQueue
}

func NewDeploymentService(
    deployments domain.DeploymentRepository,
    services domain.ServiceRepository,
    envs domain.EnvironmentRepository,
    resolver dependency.DependencyResolver,
    helmGen helmgen.HelmValuesGenerator,
    queue queue.CommandQueue,
) DeploymentService {
    return &deploymentService{
        deployments: deployments,
        services:    services,
        envs:        envs,
        resolver:    resolver,
        helmGen:     helmGen,
        queue:       queue,
    }
}
```

### 5.2 Composition Root

Each binary has a single composition root in `app.go` that wires everything together. This is the only place where concrete types are instantiated.

```go
// internal/server/app.go
func Run(ctx context.Context, cfg Config) error {
    db, err := sql.Open("postgres", cfg.DatabaseURL)
    if err != nil {
        return fmt.Errorf("open db: %w", err)
    }
    defer db.Close()

    // Repositories (concrete implementations)
    orgStore := store.NewOrganizationStore(db)
    projectStore := store.NewProjectStore(db)
    envStore := store.NewEnvironmentStore(db)
    serviceStore := store.NewServiceStore(db)
    deploymentStore := store.NewDeploymentStore(db)
    depConfigStore := store.NewDependencyConfigStore(db)
    resolvedCtxStore := store.NewResolvedContextStore(db)
    cmdQueue := queue.NewCommandQueue(db)

    // Registries
    specRegistry := dependency.NewSpecRegistry()
    providerRegistry := dependency.NewProviderRegistry()

    // Services (depend on interfaces, receive concrete impls)
    depResolver := dependency.NewDependencyResolver(depConfigStore, resolvedCtxStore, specRegistry, providerRegistry)
    helmGenerator := helmgen.NewGenerator()
    deploySvc := deployment.NewDeploymentService(deploymentStore, serviceStore, envStore, depResolver, helmGenerator, cmdQueue)

    // HTTP handlers
    router := api.NewRouter(deploySvc, serviceStore, envStore, depConfigStore, resolvedCtxStore)

    return http.ListenAndServe(cfg.ListenAddr, router)
}
```

### 5.3 Testing with Fakes

Tests use hand-written fakes (not mocks) for repository interfaces. Fakes are stored alongside the tests that use them or in a shared `internal/testutil/` package.

```go
// internal/deployment/service_test.go
func TestSubmit_CreatesDeployment(t *testing.T) {
    deps := &fakeDeploymentRepo{}
    services := &fakeServiceRepo{
        services: map[uuid.UUID]*domain.Service{
            svcID: {ID: svcID, ProjectID: projID, Name: "api"},
        },
    }
    envs := &fakeEnvironmentRepo{
        envs: map[uuid.UUID]*domain.Environment{
            envID: {ID: envID, Name: "pre", Namespace: "pre"},
        },
    }
    resolver := &fakeDependencyResolver{}
    helmGen := &fakeHelmGenerator{}
    q := &fakeQueue{}

    svc := deployment.NewDeploymentService(deps, services, envs, resolver, helmGen, q)

    d, err := svc.Submit(context.Background(), deployment.SubmitRequest{
        ProjectID:     projID,
        EnvironmentID: envID,
        ServiceConfig: domain.ServiceConfig{Name: "api", Image: "ghcr.io/org/api"},
        ImageTag:      "v1.0.0",
        TriggeredBy:   "jan",
    })

    require.NoError(t, err)
    assert.Equal(t, domain.DeploymentStatusPending, d.Status)
    assert.Len(t, q.commands, 1)
}
```

### 5.4 Error Wrapping

Errors are always wrapped with context about what operation failed. Domain sentinel errors enable callers to match with `errors.Is`.

```go
func (s *deploymentService) Submit(ctx context.Context, req SubmitRequest) (*domain.Deployment, error) {
    svc, err := s.services.GetByName(ctx, req.ProjectID, req.ServiceConfig.Name)
    if err != nil {
        return nil, fmt.Errorf("lookup service %q: %w", req.ServiceConfig.Name, err)
    }

    env, err := s.envs.GetByID(ctx, req.EnvironmentID)
    if err != nil {
        return nil, fmt.Errorf("lookup environment: %w", err)
    }

    // ...
}

// In HTTP handlers, sentinel errors map to HTTP status codes:
if errors.Is(err, domain.ErrNotFound) {
    http.Error(w, "not found", http.StatusNotFound)
    return
}
```

### 5.5 Context Propagation

All operations take `context.Context` as their first parameter. Contexts carry cancellation, deadlines, and request-scoped values (auth, trace IDs).

### 5.6 Table-Driven Tests

Repetitive test cases use table-driven style:

```go
func TestResolve_OverrideMerge(t *testing.T) {
    tests := []struct {
        name     string
        base     OverridableConfig
        envName  string
        expected domain.ServiceConfig
    }{
        {
            name:    "no override uses base",
            base:    OverridableConfig{...},
            envName: "unknown",
            expected: domain.ServiceConfig{...},
        },
        {
            name:    "replicas overridden",
            base:    OverridableConfig{...},
            envName: "pro",
            expected: domain.ServiceConfig{...},
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            resolver := config.NewResolver()
            got, err := resolver.Resolve(&tt.base, tt.envName)
            require.NoError(t, err)
            assert.Equal(t, tt.expected, *got)
        })
    }
}
```

### 5.7 Package Dependency Rule

```
domain  ←──  config, dependency, deployment, helmgen
                          ↑
              server/store, server/api, agent, cli
```

- `domain` has zero internal imports — it is leaf-level
- Business logic packages (`config`, `dependency`, `deployment`, `helmgen`) import only `domain`
- Infrastructure packages (`server/store`, `server/api`, `agent`, `cli`) import business logic interfaces and domain types
- No circular imports; the dependency graph is a DAG

---

## 6. File Summary

| File | Package | Key Types | Purpose |
|------|---------|-----------|---------|
| `internal/domain/organization.go` | domain | `Organization` | Organization entity |
| `internal/domain/project.go` | domain | `Project`, `ProjectRepository` | Project entity + repo interface |
| `internal/domain/environment.go` | domain | `Environment`, `EnvironmentRepository` | Environment entity + repo interface |
| `internal/domain/cluster.go` | domain | `Cluster`, `ClusterRepository` | Cluster entity + repo interface |
| `internal/domain/service.go` | domain | `Service`, `ServiceRepository` | Service entity + repo interface |
| `internal/domain/deployment.go` | domain | `Deployment`, `DeploymentStatus`, `DeploymentRepository` | Deployment entity + status enum + repo interface |
| `internal/domain/dependency.go` | domain | `DependencyDeclaration`, `DependencyConfig`, `ResolvedContext`, `DependencySpec`, `FieldSpec`, `DependencyConfigRepository`, `ResolvedContextRepository` | All dependency-related domain types + repo interfaces |
| `internal/domain/serviceconfig.go` | domain | `ServiceConfig`, `IngressConfig`, `ServiceSpec`, `ManagementConfig`, `ConfigFileSpec` | Resolved service configuration struct tree |
| `internal/domain/errors.go` | domain | `ErrNotFound`, `ErrAlreadyExists`, `ValidationErrors`, `ValidationError` | Sentinel errors + validation aggregator |
| `internal/config/interfaces.go` | config | `ConfigParser`, `ConfigResolver`, `TemplateRenderer` | Config processing interfaces |
| `internal/config/parser.go` | config | `parser` (unexported) | YAML parsing of pond.yml |
| `internal/config/resolver.go` | config | `resolver` (unexported) | Override merge logic |
| `internal/config/template.go` | config | `templateRenderer` (unexported) | `{{var}}` interpolation |
| `internal/dependency/interfaces.go` | dependency | `DependencyResolver`, `ManagedProvider`, `SpecRegistry`, `ProviderRegistry` | Dependency domain interfaces |
| `internal/dependency/registry.go` | dependency | `specRegistry`, `providerRegistry` (unexported) | In-memory registries |
| `internal/dependency/context.go` | dependency | `dependencyResolver` (unexported) | Resolves all deps for a deployment |
| `internal/dependency/specs.go` | dependency | — | Registers built-in specs (postgres, etc.) |
| `internal/dependency/provider/tofu/provider.go` | tofu | `Provider` | OpenTofu managed provider impl |
| `internal/deployment/interfaces.go` | deployment | `DeploymentService`, `SubmitRequest`, `ValidationResult` | Deployment orchestration interface |
| `internal/deployment/service.go` | deployment | `deploymentService` (unexported) | Core deployment orchestration |
| `internal/deployment/validation.go` | deployment | — | Pre-deploy validation logic |
| `internal/helmgen/interfaces.go` | helmgen | `HelmValuesGenerator` | Helm generation interface |
| `internal/helmgen/generator.go` | helmgen | `generator` (unexported) | ServiceConfig → HelmValues mapping |
| `internal/helmgen/types.go` | helmgen | `HelmValues`, `Image`, `Ingress`, `Probe`, etc. | Helm values struct tree |
| `internal/server/api/router.go` | api | — | HTTP router setup |
| `internal/server/api/deployment_handler.go` | api | `DeploymentHandler` | REST handlers for deployments |
| `internal/server/api/dependency_handler.go` | api | `DependencyHandler` | REST handlers for dependency config |
| `internal/server/store/*.go` | store | `OrganizationStore`, `ProjectStore`, etc. | PostgreSQL repository implementations |
| `internal/server/queue/queue.go` | queue | `CommandQueue` | Agent command queue |
| `internal/server/app.go` | server | — | Server composition root |
| `internal/agent/connection.go` | agent | `connection` (unexported) | gRPC stream to server |
| `internal/agent/executor.go` | agent | `executor` (unexported) | Command dispatch |
| `internal/agent/helm/runner.go` | helm | `runner` (unexported) | Helm CLI wrapper |
| `internal/agent/tofu/runner.go` | tofu | `runner` (unexported) | OpenTofu CLI wrapper |
| `internal/agent/app.go` | agent | — | Agent composition root |
| `internal/cli/commands/*.go` | commands | — | Cobra command definitions |
| `internal/cli/client/client.go` | client | `httpClient` (unexported) | HTTP client to server API |
| `internal/cli/app.go` | cli | — | CLI composition root |
| `cmd/pond-cli/main.go` | main | — | CLI entrypoint |
| `cmd/pond-server/main.go` | main | — | Server entrypoint |
| `cmd/pond-agent/main.go` | main | — | Agent entrypoint |

---

## 7. Config Processing Pipeline

The config pipeline is a clear sequence of transformations, each handled by a distinct interface:

```
pond.yml (file on disk)
    │
    ▼  ConfigParser.ParseFile()
OverridableConfig
    │
    ▼  ConfigResolver.Resolve(envName)
ServiceConfig  (overrides applied)
    │
    ├─▶ DependencyResolver.ResolveAll()  →  map[string]ResolvedContext
    │
    ▼  TemplateRenderer.Render()
ServiceConfig  (templates interpolated)
    │
    ▼  HelmValuesGenerator.Generate()
HelmValues  (ready for Helm)
```

### `OverridableConfig` (internal to config package)

```go
// internal/config/types.go

type OverridableConfig struct {
    domain.ServiceConfig `yaml:",inline"`
    Overrides            map[string]Override `yaml:"overrides"`
}

type Override struct {
    Ingress *IngressOverride `yaml:"ingress"`
    Service *ServiceOverride `yaml:"service"`
    Env     map[string]string                       `yaml:"env"`
    Dependencies map[string]domain.DependencyDeclaration `yaml:"dependencies"`
    Configs      map[string]domain.ConfigFileSpec         `yaml:"configs"`
}

type IngressOverride struct {
    Enabled *bool `yaml:"enabled"`
}

type ServiceOverride struct {
    Replicas *int32 `yaml:"replicas"`
}
```

Override merge rules:
- Pointer fields (`*bool`, `*int32`): replace base when non-nil
- `Port` and `Management`: not overridable
- Maps (`Env`, `Dependencies`, `Configs`): deep merge, override keys win

---

## 8. Helm Values Types (`internal/helmgen/types.go`)

```go
type HelmValues struct {
    ReplicaCount     int               `yaml:"replicaCount"`
    Image            Image             `yaml:"image"`
    NameOverride     string            `yaml:"nameOverride"`
    FullnameOverride string            `yaml:"fullnameOverride"`
    Service          HelmService       `yaml:"service"`
    Ingress          HelmIngress       `yaml:"ingress"`
    LivenessProbe    *HelmProbe        `yaml:"livenessProbe,omitempty"`
    ReadinessProbe   *HelmProbe        `yaml:"readinessProbe,omitempty"`
    Env              map[string]string `yaml:"env"`
    Configs          []HelmConfig      `yaml:"configs"`
    PodAnnotations   map[string]string `yaml:"podAnnotations,omitempty"`
    PodLabels        map[string]string `yaml:"podLabels,omitempty"`
}

type Image struct {
    Repository string `yaml:"repository"`
    Tag        string `yaml:"tag"`
    PullPolicy string `yaml:"pullPolicy"`
}

type HelmService struct {
    Type string `yaml:"type"`
    Port int    `yaml:"port"`
}

type HelmIngress struct {
    Enabled     bool              `yaml:"enabled"`
    ClassName   string            `yaml:"className"`
    Annotations map[string]string `yaml:"annotations,omitempty"`
    Hosts       []HelmIngressHost `yaml:"hosts"`
    TLS         []HelmIngressTLS  `yaml:"tls,omitempty"`
}

type HelmIngressHost struct {
    Host  string            `yaml:"host"`
    Paths []HelmIngressPath `yaml:"paths"`
}

type HelmIngressPath struct {
    Path     string `yaml:"path"`
    PathType string `yaml:"pathType"`
}

type HelmIngressTLS struct {
    Hosts      []string `yaml:"hosts"`
    SecretName string   `yaml:"secretName"`
}

type HelmProbe struct {
    HTTPGet HelmHTTPGet `yaml:"httpGet"`
}

type HelmHTTPGet struct {
    Path string `yaml:"path"`
    Port int    `yaml:"port"`
}

type HelmConfig struct {
    Enabled       bool   `yaml:"enabled"`
    MountLocation string `yaml:"mountLocation"`
    Data          string `yaml:"data"` // base64-encoded
}
```

---

## 9. Agent Command Protocol

Commands flow from server to agent over a persistent gRPC stream. Each command is self-contained.

```go
// internal/agent/types.go

type CommandType string

const (
    CommandHelmUpgrade CommandType = "helm.upgrade"
    CommandTofuApply   CommandType = "tofu.apply"
    CommandTofuOutput  CommandType = "tofu.output"
    CommandStatusQuery CommandType = "status.query"
)

type Command struct {
    ID           uuid.UUID
    DeploymentID uuid.UUID
    Type         CommandType
    Payload      json.RawMessage // type-specific payload
    CreatedAt    time.Time
}

type CommandResult struct {
    CommandID uuid.UUID
    Success   bool
    Output    json.RawMessage // type-specific output
    Error     string
}

type LogEntry struct {
    CommandID uuid.UUID
    Line      string
    Timestamp time.Time
    Stream    string // "stdout" | "stderr"
}
```

---

## 10. Testing Strategy

| Layer | Test Type | Dependencies |
|-------|-----------|--------------|
| `domain` | Unit | None — pure structs |
| `config` | Unit | File fixtures for pond.yml |
| `dependency` | Unit | Fake repositories, fake providers |
| `deployment` | Unit | Fake repos, fake resolver, fake helmgen, fake queue |
| `helmgen` | Unit | None — pure transformation |
| `server/store` | Integration | Test PostgreSQL database (testcontainers or similar) |
| `server/api` | Integration | Fake services injected into handlers |
| `agent/helm` | Integration | Helm CLI (optional, can be faked) |
| `agent/tofu` | Integration | OpenTofu CLI (optional, can be faked) |
| End-to-end | E2E | Full stack with test cluster |

Business logic tests (config, dependency, deployment, helmgen) must run without any external dependencies — they use only fakes/in-memory implementations.

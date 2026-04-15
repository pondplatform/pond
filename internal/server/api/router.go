package api

import (
	"net/http"

	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

// RouterDeps groups all dependencies needed by the router.
type RouterDeps struct {
	DeploySvc    service.DeploymentService
	Orgs         store.OrganizationRepository
	Projects     store.ProjectRepository
	Envs         store.EnvironmentRepository
	Services     store.ServiceRepository
	Clusters     store.ClusterRepository
	SpecRegistry dependency.SpecRegistry
	AgentHandler *AgentHandler
}

func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()

	// Handlers
	deployHandler := NewDeploymentHandler(deps.DeploySvc, deps.Services)
	orgHandler := NewOrganizationHandler(deps.Orgs)
	clusterHandler := NewClusterHandler(deps.Clusters, deps.Orgs)
	projectHandler := NewProjectHandler(deps.Projects, deps.Orgs, deps.Envs)
	envHandler := NewEnvironmentHandler(deps.Envs, deps.Projects, deps.Clusters)
	serviceHandler := NewServiceHandler(deps.Services, deps.Projects)
	depSpecHandler := NewDependencySpecHandler(deps.SpecRegistry)

	// --- Existing deployment routes (backward compatible) ---
	mux.HandleFunc("POST /deployments", deployHandler.Submit)
	mux.HandleFunc("GET /deployments/{id}", deployHandler.GetStatus)
	mux.HandleFunc("POST /deployments/{id}/dependencies/{name}/input", deployHandler.ProvideUserInput)
	mux.HandleFunc("POST /deployments/{id}/cancel", deployHandler.Cancel)

	// --- API v1 routes ---

	// Organizations
	mux.HandleFunc("POST /api/v1/organizations", orgHandler.Create)
	mux.HandleFunc("GET /api/v1/organizations", orgHandler.List)
	mux.HandleFunc("GET /api/v1/organizations/{id}", orgHandler.Get)

	// Clusters (scoped under org)
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/clusters", clusterHandler.Create)
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/clusters", clusterHandler.List)
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/clusters/{id}", clusterHandler.Get)
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/clusters/{id}/rotate-token", clusterHandler.RotateToken)

	// Projects (scoped under org)
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/projects", projectHandler.Create)
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/projects", projectHandler.List)
	mux.HandleFunc("GET /api/v1/projects/{id}", projectHandler.Get)
	mux.HandleFunc("PATCH /api/v1/projects/{id}", projectHandler.Update)

	// Environments (scoped under project)
	mux.HandleFunc("POST /api/v1/projects/{projectId}/environments", envHandler.Create)
	mux.HandleFunc("GET /api/v1/projects/{projectId}/environments", envHandler.List)
	mux.HandleFunc("GET /api/v1/environments/{id}", envHandler.Get)
	mux.HandleFunc("PATCH /api/v1/environments/{id}", envHandler.Update)

	// Services (read-only, scoped under project)
	mux.HandleFunc("GET /api/v1/projects/{projectId}/services", serviceHandler.List)
	mux.HandleFunc("GET /api/v1/services/{id}", serviceHandler.Get)

	// Deployments (additional v1 routes)
	mux.HandleFunc("GET /api/v1/services/{serviceId}/deployments", deployHandler.ListByService)
	mux.HandleFunc("POST /api/v1/deployments/validate", deployHandler.Validate)

	// Dependency specs (static registry)
	mux.HandleFunc("GET /api/v1/dependency-specs", depSpecHandler.List)
	mux.HandleFunc("GET /api/v1/dependency-specs/{type}", depSpecHandler.Get)

	// Agent WebSocket endpoint — registered before middleware to bypass jsonContentType.
	mux.HandleFunc("GET /agent/ws", deps.AgentHandler.ServeWS)

	// Apply middleware to all routes.
	var handler http.Handler = mux
	handler = jsonContentType(handler)
	handler = loggingMiddleware(handler)

	return handler
}

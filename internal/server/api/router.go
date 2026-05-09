package api

import (
	"log/slog"
	"net/http"

	"github.com/pondplatform/pond/internal/server/auth"
	"github.com/pondplatform/pond/internal/server/dependency"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

// RouterDeps groups all dependencies needed by the router.
type RouterDeps struct {
	DeploySvc     service.DeploymentService
	Orgs          store.OrganizationRepository
	Projects      store.ProjectRepository
	Envs          store.EnvironmentRepository
	Services      store.ServiceRepository
	Clusters      store.ClusterRepository
	JWTSecret     []byte
	SpecRegistry  dependency.SpecRegistry
	AgentHandler  *AgentHandler
	Authenticator auth.Authenticator
	Authorizer    auth.Authorizer
	Log           *slog.Logger
}

func NewRouter(deps RouterDeps) http.Handler {
	mux := http.NewServeMux()

	// Handlers
	deployHandler := NewDeploymentHandler(deps.DeploySvc, deps.Services, deps.Log)
	orgHandler := NewOrganizationHandler(deps.Orgs, deps.Log)
	clusterHandler := NewClusterHandler(deps.Clusters, deps.Orgs, deps.Log)
	projectHandler := NewProjectHandler(deps.Projects, deps.Orgs, deps.Envs, deps.Log)
	envHandler := NewEnvironmentHandler(deps.Envs, deps.Projects, deps.Clusters, deps.Log)
	serviceHandler := NewServiceHandler(deps.Services, deps.Projects, deps.Log)
	depSpecHandler := NewDependencySpecHandler(deps.SpecRegistry, deps.Log)
	tokenHandler := NewTokenHandler(deps.JWTSecret, deps.Log)

	// Auth middleware helpers
	authed := func(action auth.Action, pathParam string, h http.HandlerFunc) http.HandlerFunc {
		return chain(
			requireAuth(deps.Authenticator, deps.Log),
			requireOrgAccess(deps.Authorizer, action, pathParam, deps.Log),
		)(http.HandlerFunc(h)).ServeHTTP
	}

	// Shorthand for org-scoped routes (orgId in path)
	orgRead := func(h http.HandlerFunc) http.HandlerFunc {
		return authed(auth.Action{Resource: auth.ResourceOrganization, Verb: auth.VerbRead}, "orgId", h)
	}
	orgManage := func(h http.HandlerFunc) http.HandlerFunc {
		return authed(auth.Action{Resource: auth.ResourceOrganization, Verb: auth.VerbManage}, "orgId", h)
	}

	// Shorthand for routes where org is implicit (no orgId in path)
	implicitRead := func(resource auth.ResourceType, h http.HandlerFunc) http.HandlerFunc {
		return authed(auth.Action{Resource: resource, Verb: auth.VerbRead}, "", h)
	}
	implicitWrite := func(resource auth.ResourceType, h http.HandlerFunc) http.HandlerFunc {
		return authed(auth.Action{Resource: resource, Verb: auth.VerbWrite}, "", h)
	}

	// --- API v1 routes ---

	// Deployments
	mux.HandleFunc("POST /api/v1/deployments", implicitWrite(auth.ResourceDeployment, deployHandler.Submit))
	mux.HandleFunc("GET /api/v1/deployments/{id}", implicitRead(auth.ResourceDeployment, deployHandler.GetStatus))
	mux.HandleFunc("POST /api/v1/deployments/{id}/dependencies/{name}/input", implicitWrite(auth.ResourceDeployment, deployHandler.ProvideUserInput))
	mux.HandleFunc("POST /api/v1/deployments/{id}/cancel", implicitWrite(auth.ResourceDeployment, deployHandler.Cancel))
	mux.HandleFunc("GET /api/v1/commands/{commandId}/logs", implicitRead(auth.ResourceDeployment, deployHandler.GetCommandLogs))

	// Organizations
	mux.HandleFunc("POST /api/v1/organizations", authed(auth.Action{Resource: auth.ResourceOrganization, Verb: auth.VerbManage}, "", orgHandler.Create))
	mux.HandleFunc("GET /api/v1/organizations", authed(auth.Action{Resource: auth.ResourceOrganization, Verb: auth.VerbRead}, "", orgHandler.List))
	mux.HandleFunc("GET /api/v1/organizations/{id}", authed(auth.Action{Resource: auth.ResourceOrganization, Verb: auth.VerbRead}, "", orgHandler.Get))

	// Clusters (scoped under org)
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/clusters", orgManage(clusterHandler.Create))
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/clusters", orgRead(clusterHandler.List))
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/clusters/{id}", orgRead(clusterHandler.Get))
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/clusters/{id}/rotate-token", orgManage(clusterHandler.RotateToken))

	// API Tokens (scoped under org, admin-only)
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/tokens", authed(auth.Action{Resource: auth.ResourceToken, Verb: auth.VerbManage}, "orgId", tokenHandler.Create))

	// Projects (scoped under org)
	mux.HandleFunc("POST /api/v1/organizations/{orgId}/projects", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbWrite}, "orgId", projectHandler.Create))
	mux.HandleFunc("GET /api/v1/organizations/{orgId}/projects", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbRead}, "orgId", projectHandler.List))
	mux.HandleFunc("GET /api/v1/projects/{id}", implicitRead(auth.ResourceProject, projectHandler.Get))
	mux.HandleFunc("PATCH /api/v1/projects/{id}", implicitWrite(auth.ResourceProject, projectHandler.Update))

	// Environments (scoped under project)
	mux.HandleFunc("POST /api/v1/projects/{projectId}/environments", implicitWrite(auth.ResourceEnvironment, envHandler.Create))
	mux.HandleFunc("GET /api/v1/projects/{projectId}/environments", implicitRead(auth.ResourceEnvironment, envHandler.List))
	mux.HandleFunc("GET /api/v1/environments/{id}", implicitRead(auth.ResourceEnvironment, envHandler.Get))
	mux.HandleFunc("PATCH /api/v1/environments/{id}", implicitWrite(auth.ResourceEnvironment, envHandler.Update))

	// Services (read-only, scoped under project)
	mux.HandleFunc("GET /api/v1/projects/{projectId}/services", implicitRead(auth.ResourceService, serviceHandler.List))
	mux.HandleFunc("GET /api/v1/services/{id}", implicitRead(auth.ResourceService, serviceHandler.Get))

	// Deployments (additional v1 routes)
	mux.HandleFunc("GET /api/v1/services/{serviceId}/deployments", implicitRead(auth.ResourceDeployment, deployHandler.ListByService))
	mux.HandleFunc("POST /api/v1/deployments/validate", implicitRead(auth.ResourceDeployment, deployHandler.Validate))

	// Dependency specs (static registry - public read)
	mux.HandleFunc("GET /api/v1/dependency-specs", implicitRead(auth.ResourceDependency, depSpecHandler.List))
	mux.HandleFunc("GET /api/v1/dependency-specs/{type}", implicitRead(auth.ResourceDependency, depSpecHandler.Get))

	// Agent WebSocket endpoint — uses its own cluster-token auth, NOT API token auth.
	mux.HandleFunc("GET /agent/ws", deps.AgentHandler.ServeWS)

	// Apply global middleware (logging, content-type) to all routes.
	var handler http.Handler = mux
	handler = jsonContentType(handler)
	handler = loggingMiddleware(deps.Log)(handler)

	return handler
}

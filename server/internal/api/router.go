package api

import (
	"log/slog"
	"net/http"

	"github.com/pondplatform/pond/server/internal/auth"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
)

// RouterDeps groups all dependencies needed by the router.
type RouterDeps struct {
	DeploySvc     service.DeploymentService
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
	deployHandler := NewDeploymentHandler(deps.DeploySvc, deps.Services, deps.Authorizer, deps.Log)
	clusterHandler := NewClusterHandler(deps.Clusters, deps.Log)
	projectHandler := NewProjectHandler(deps.Projects, deps.Envs, deps.Log)
	envHandler := NewEnvironmentHandler(deps.Envs, deps.Projects, deps.Clusters, deps.Log)
	serviceHandler := NewServiceHandler(deps.Services, deps.Projects, deps.Log)
	depSpecHandler := NewDependencySpecHandler(deps.SpecRegistry, deps.Log)
	tokenHandler := NewTokenHandler(deps.JWTSecret, deps.Log)

	// authed wraps a handler with authentication + resource-level authorization.
	//   resourceParam: path key for the specific resource to ownership-check, or "".
	authed := func(action auth.Action, resourceParam string, h http.HandlerFunc) http.HandlerFunc {
		return chain(
			requireAuth(deps.Authenticator, deps.Log),
			requireResourceAccess(deps.Authorizer, action, resourceParam, deps.Log),
		)(http.HandlerFunc(h)).ServeHTTP
	}

	// --- API v1 routes ---

	// Deployments
	mux.HandleFunc("POST /api/v1/deployments", authed(auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbWrite}, "", deployHandler.Submit))
	mux.HandleFunc("GET /api/v1/deployments/{deploymentId}", authed(auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbRead}, "deploymentId", deployHandler.GetStatus))
	mux.HandleFunc("POST /api/v1/deployments/{deploymentId}/user-input", authed(auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbWrite}, "deploymentId", deployHandler.ConfigureDeployment))
	mux.HandleFunc("POST /api/v1/deployments/{deploymentId}/cancel", authed(auth.Action{Resource: auth.ResourceDeployment, Verb: auth.VerbWrite}, "deploymentId", deployHandler.Cancel))
	mux.HandleFunc("GET /api/v1/commands/{commandId}/logs", authed(auth.Action{Resource: auth.ResourceCommand, Verb: auth.VerbRead}, "commandId", deployHandler.GetCommandLogs))

	// Clusters
	mux.HandleFunc("POST /api/v1/clusters", authed(auth.Action{Resource: auth.ResourceCluster, Verb: auth.VerbManage}, "", clusterHandler.Create))
	mux.HandleFunc("GET /api/v1/clusters", authed(auth.Action{Resource: auth.ResourceCluster, Verb: auth.VerbRead}, "", clusterHandler.List))
	mux.HandleFunc("GET /api/v1/clusters/{clusterId}", authed(auth.Action{Resource: auth.ResourceCluster, Verb: auth.VerbRead}, "clusterId", clusterHandler.Get))
	mux.HandleFunc("POST /api/v1/clusters/{clusterId}/rotate-token", authed(auth.Action{Resource: auth.ResourceCluster, Verb: auth.VerbManage}, "clusterId", clusterHandler.RotateToken))

	// API Tokens (admin-only)
	mux.HandleFunc("POST /api/v1/tokens", authed(auth.Action{Resource: auth.ResourceToken, Verb: auth.VerbManage}, "", tokenHandler.Create))

	// Projects
	mux.HandleFunc("POST /api/v1/projects", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbWrite}, "", projectHandler.Create))
	mux.HandleFunc("GET /api/v1/projects", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbRead}, "", projectHandler.List))
	mux.HandleFunc("GET /api/v1/projects/{projectId}", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbRead}, "projectId", projectHandler.Get))
	mux.HandleFunc("PATCH /api/v1/projects/{projectId}", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbWrite}, "projectId", projectHandler.Update))

	// Environments (scoped under project)
	mux.HandleFunc("POST /api/v1/projects/{projectId}/environments", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbWrite}, "projectId", envHandler.Create))
	mux.HandleFunc("GET /api/v1/projects/{projectId}/environments", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbRead}, "projectId", envHandler.List))
	mux.HandleFunc("GET /api/v1/environments/{envId}", authed(auth.Action{Resource: auth.ResourceEnvironment, Verb: auth.VerbRead}, "envId", envHandler.Get))
	mux.HandleFunc("PATCH /api/v1/environments/{envId}", authed(auth.Action{Resource: auth.ResourceEnvironment, Verb: auth.VerbWrite}, "envId", envHandler.Update))

	// Services (read-only, scoped under project)
	mux.HandleFunc("GET /api/v1/projects/{projectId}/services", authed(auth.Action{Resource: auth.ResourceProject, Verb: auth.VerbRead}, "projectId", serviceHandler.List))
	mux.HandleFunc("GET /api/v1/services/{serviceId}", authed(auth.Action{Resource: auth.ResourceService, Verb: auth.VerbRead}, "serviceId", serviceHandler.Get))

	// Deployments by service
	mux.HandleFunc("GET /api/v1/services/{serviceId}/deployments", authed(auth.Action{Resource: auth.ResourceService, Verb: auth.VerbRead}, "serviceId", deployHandler.ListByService))

	// Dependency specs (static registry - public read)
	mux.HandleFunc("GET /api/v1/dependency-specs", authed(auth.Action{Resource: auth.ResourceDependency, Verb: auth.VerbRead}, "", depSpecHandler.List))
	mux.HandleFunc("GET /api/v1/dependency-specs/{type}", authed(auth.Action{Resource: auth.ResourceDependency, Verb: auth.VerbRead}, "", depSpecHandler.Get))

	// Agent WebSocket endpoint — uses its own cluster-token auth, NOT API token auth.
	mux.HandleFunc("GET /agent/ws", deps.AgentHandler.ServeWS)

	// Apply global middleware (logging, content-type) to all routes.
	var handler http.Handler = mux
	handler = jsonContentType(handler)
	handler = loggingMiddleware(deps.Log)(handler)

	return handler
}

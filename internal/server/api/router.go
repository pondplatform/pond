package api

import (
	"net/http"

	"github.com/pondplatform/pond/internal/common/deployment"
	"github.com/pondplatform/pond/internal/common/domain"
)

func NewRouter(
	deploySvc deployment.DeploymentService,
	services domain.ServiceRepository,
	envs domain.EnvironmentRepository,
	depConfigs domain.DependencyConfigRepository,
	resolvedCtxs domain.ResolvedContextRepository,
	agentHandler *AgentHandler,
) http.Handler {
	mux := http.NewServeMux()

	deployHandler := NewDeploymentHandler(deploySvc)
	depHandler := NewDependencyHandler(depConfigs, resolvedCtxs)
	envHandler := NewEnvironmentHandler(envs)
	svcHandler := NewServiceHandler(services)

	// Deployment routes
	mux.HandleFunc("POST /deployments", deployHandler.Submit)
	mux.HandleFunc("GET /deployments/{id}", deployHandler.GetStatus)

	// Environment routes
	mux.HandleFunc("GET /projects/{projectID}/environments", envHandler.ListByProject)

	// Service routes
	mux.HandleFunc("GET /projects/{projectID}/services", svcHandler.ListByProject)

	// Dependency routes
	mux.HandleFunc("PUT /services/{serviceID}/environments/{envID}/dependencies/{name}", depHandler.SetConfig)
	mux.HandleFunc("GET /services/{serviceID}/environments/{envID}/dependencies/{name}", depHandler.GetConfig)
	mux.HandleFunc("GET /services/{serviceID}/environments/{envID}/dependencies/{name}/context", depHandler.GetContext)

	// Agent WebSocket endpoint — registered before middleware to bypass jsonContentType.
	mux.HandleFunc("GET /agent/ws", agentHandler.ServeWS)

	// Apply middleware to all routes.
	var handler http.Handler = mux
	handler = jsonContentType(handler)
	handler = loggingMiddleware(handler)

	return handler
}

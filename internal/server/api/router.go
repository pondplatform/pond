package api

import (
	"net/http"

	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

func NewRouter(
	deploySvc service.DeploymentService,
	services store.ServiceRepository,
	envs store.EnvironmentRepository,
	agentHandler *AgentHandler,
) http.Handler {
	mux := http.NewServeMux()

	deployHandler := NewDeploymentHandler(deploySvc)

	// Deployment routes
	mux.HandleFunc("POST /deployments", deployHandler.Submit)
	mux.HandleFunc("GET /deployments/{id}", deployHandler.GetStatus)

	// Agent WebSocket endpoint — registered before middleware to bypass jsonContentType.
	mux.HandleFunc("GET /agent/ws", agentHandler.ServeWS)

	// Apply middleware to all routes.
	var handler http.Handler = mux
	handler = jsonContentType(handler)
	handler = loggingMiddleware(handler)

	return handler
}

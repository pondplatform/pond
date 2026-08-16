package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
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
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(GinLogger(deps.Log), gin.Recovery())

	deployHandler := NewDeploymentHandler(deps.DeploySvc, deps.Services, deps.Authorizer, deps.Log)
	clusterHandler := NewClusterHandler(deps.Clusters, deps.Log)
	projectHandler := NewProjectHandler(deps.Projects, deps.Envs, deps.Log)
	envHandler := NewEnvironmentHandler(deps.Envs, deps.Projects, deps.Clusters, deps.Log)
	serviceHandler := NewServiceHandler(deps.Services, deps.Projects, deps.Log)
	depSpecHandler := NewDependencySpecHandler(deps.SpecRegistry, deps.Log)
	tokenHandler := NewTokenHandler(deps.JWTSecret, deps.Log)

	// Agent WebSocket uses its own cluster-token auth, not API token auth.
	r.GET("/agent/ws", deps.AgentHandler.ServeWS)

	// All /api/v1 routes require a valid API token.
	v1 := r.Group("/api/v1")
	v1.Use(GinRequireAuth(deps.Authenticator, deps.Log))

	az := func(resource auth.ResourceType, verb auth.Verb) gin.HandlerFunc {
		return GinAuthorize(deps.Authorizer, auth.Action{Resource: resource, Verb: verb})
	}

	// Deployments
	v1.POST("/deployments", az(auth.ResourceDeployment, auth.VerbWrite), deployHandler.Submit)
	v1.GET("/deployments/:deploymentId", az(auth.ResourceDeployment, auth.VerbRead), deployHandler.GetStatus)
	v1.POST("/deployments/:deploymentId/user-input", az(auth.ResourceDeployment, auth.VerbWrite), deployHandler.ConfigureDeployment)
	v1.POST("/deployments/:deploymentId/cancel", az(auth.ResourceDeployment, auth.VerbWrite), deployHandler.Cancel)
	v1.GET("/commands/:commandId/logs", az(auth.ResourceCommand, auth.VerbRead), deployHandler.GetCommandLogs)

	// Clusters (manage = admin-only)
	v1.POST("/clusters", az(auth.ResourceCluster, auth.VerbManage), clusterHandler.Create)
	v1.GET("/clusters", az(auth.ResourceCluster, auth.VerbRead), clusterHandler.List)
	v1.GET("/clusters/:clusterId", az(auth.ResourceCluster, auth.VerbRead), clusterHandler.Get)
	v1.POST("/clusters/:clusterId/rotate-token", az(auth.ResourceCluster, auth.VerbManage), clusterHandler.RotateToken)

	// API Tokens (admin-only)
	v1.POST("/tokens", az(auth.ResourceToken, auth.VerbManage), tokenHandler.Create)

	// Projects
	v1.POST("/projects", az(auth.ResourceProject, auth.VerbWrite), projectHandler.Create)
	v1.GET("/projects", az(auth.ResourceProject, auth.VerbRead), projectHandler.List)
	v1.GET("/projects/:projectId", az(auth.ResourceProject, auth.VerbRead), projectHandler.Get)
	v1.PATCH("/projects/:projectId", az(auth.ResourceProject, auth.VerbWrite), projectHandler.Update)

	// Environments
	v1.POST("/projects/:projectId/environments", az(auth.ResourceProject, auth.VerbWrite), envHandler.Create)
	v1.GET("/projects/:projectId/environments", az(auth.ResourceProject, auth.VerbRead), envHandler.List)
	v1.GET("/environments/:envId", az(auth.ResourceEnvironment, auth.VerbRead), envHandler.Get)
	v1.PATCH("/environments/:envId", az(auth.ResourceEnvironment, auth.VerbWrite), envHandler.Update)

	// Services
	v1.GET("/projects/:projectId/services", az(auth.ResourceProject, auth.VerbRead), serviceHandler.List)
	v1.GET("/services/:serviceId", az(auth.ResourceService, auth.VerbRead), serviceHandler.Get)
	v1.GET("/services/:serviceId/deployments", az(auth.ResourceService, auth.VerbRead), deployHandler.ListByService)

	// Dependency specs
	v1.GET("/dependency-specs", az(auth.ResourceDependency, auth.VerbRead), depSpecHandler.List)
	v1.GET("/dependency-specs/:type", az(auth.ResourceDependency, auth.VerbRead), depSpecHandler.Get)

	return r
}

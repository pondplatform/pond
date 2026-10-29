package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

type ServiceHandler struct {
	services    store.ServiceRepository
	projects    store.ProjectRepository
	deployments store.DeploymentInfoStore
	log         *slog.Logger
}

func NewServiceHandler(services store.ServiceRepository, projects store.ProjectRepository, deployments store.DeploymentInfoStore, log *slog.Logger) *ServiceHandler {
	return &ServiceHandler{services: services, projects: projects, deployments: deployments, log: log}
}

// toServiceResponse gives s the status derived from latest, its latest
// deployment in each environment.
func toServiceResponse(s *domain.Service, latest []domain.Deployment) api.Service {
	return api.Service{
		ID:                  s.ID,
		ProjectID:           s.ProjectID,
		Name:                s.Name,
		CurrentDeploymentID: s.CurrentDeploymentID,
		RunningDeploymentID: s.RunningDeploymentID,
		Status:              domain.ServiceStatus(latest),
		CreatedAt:           s.CreatedAt,
	}
}

func (h *ServiceHandler) Get(c *gin.Context) {
	id, ok := parseUUIDParam(c, "serviceId", "service id")
	if !ok {
		return
	}
	svc, err := h.get(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusOK, svc)
}

func (h *ServiceHandler) get(ctx context.Context, id uuid.UUID) (api.Service, error) {
	svc, err := h.services.GetByID(ctx, id)
	if err != nil {
		return api.Service{}, err
	}
	latest, err := h.latestByService(ctx, svc.ProjectID)
	if err != nil {
		return api.Service{}, err
	}
	return toServiceResponse(svc, latest[svc.ID]), nil
}

func (h *ServiceHandler) List(c *gin.Context) {
	projectID, ok := parseUUIDParam(c, "projectId", "project id")
	if !ok {
		return
	}
	items, err := h.list(c.Request.Context(), projectID)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeList(c, items, nil)
}

func (h *ServiceHandler) list(ctx context.Context, projectID uuid.UUID) ([]api.Service, error) {
	if _, err := h.projects.GetByID(ctx, projectID); err != nil {
		return nil, err
	}

	services, err := h.services.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	latest, err := h.latestByService(ctx, projectID)
	if err != nil {
		return nil, err
	}

	items := make([]api.Service, len(services))
	for i, s := range services {
		items[i] = toServiceResponse(&s, latest[s.ID])
	}
	return items, nil
}

// latestByService groups the latest deployment of every service of the
// project in each environment by service.
func (h *ServiceHandler) latestByService(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID][]domain.Deployment, error) {
	deployments, err := h.deployments.ListLatestByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	byService := make(map[uuid.UUID][]domain.Deployment)
	for _, d := range deployments {
		byService[d.ServiceID] = append(byService[d.ServiceID], d)
	}
	return byService, nil
}

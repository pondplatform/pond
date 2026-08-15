package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

type ServiceHandler struct {
	services store.ServiceRepository
	projects store.ProjectRepository
	log      *slog.Logger
}

func NewServiceHandler(services store.ServiceRepository, projects store.ProjectRepository, log *slog.Logger) *ServiceHandler {
	return &ServiceHandler{services: services, projects: projects, log: log}
}

func toServiceResponse(s *domain.Service) api.Service {
	return api.Service{
		ID:                  s.ID,
		ProjectID:           s.ProjectID,
		Name:                s.Name,
		CurrentDeploymentID: s.CurrentDeploymentID,
		RunningDeploymentID: s.RunningDeploymentID,
		CreatedAt:           s.CreatedAt,
	}
}

func (h *ServiceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("serviceId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}
	svc, err := h.get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, toServiceResponse(svc))
}

func (h *ServiceHandler) get(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	return h.services.GetByID(ctx, id)
}

func (h *ServiceHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	items, err := h.list(r.Context(), projectID)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeList(w, items, nil)
}

func (h *ServiceHandler) list(ctx context.Context, projectID uuid.UUID) ([]api.Service, error) {
	if _, err := h.projects.GetByID(ctx, projectID); err != nil {
		return nil, err
	}

	services, err := h.services.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	items := make([]api.Service, len(services))
	for i, s := range services {
		items[i] = toServiceResponse(&s)
	}
	return items, nil
}

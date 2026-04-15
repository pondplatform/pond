package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type ServiceHandler struct {
	services store.ServiceRepository
	projects store.ProjectRepository
}

func NewServiceHandler(services store.ServiceRepository, projects store.ProjectRepository) *ServiceHandler {
	return &ServiceHandler{services: services, projects: projects}
}

func (h *ServiceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}

	svc, err := h.services.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, svc)
}

func (h *ServiceHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	// Verify project exists
	if _, err := h.projects.GetByID(r.Context(), projectID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	services, err := h.services.ListByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeList(w, services, nil)
}

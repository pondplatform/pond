package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type ServiceHandler struct {
	services domain.ServiceRepository
}

func NewServiceHandler(services domain.ServiceRepository) *ServiceHandler {
	return &ServiceHandler{services: services}
}

func (h *ServiceHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	svcs, err := h.services.ListByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(svcs)
}

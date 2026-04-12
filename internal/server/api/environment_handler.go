package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/server/store"
)

type EnvironmentHandler struct {
	envs store.EnvironmentRepository
}

func NewEnvironmentHandler(envs store.EnvironmentRepository) *EnvironmentHandler {
	return &EnvironmentHandler{envs: envs}
}

func (h *EnvironmentHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectID"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	envs, err := h.envs.ListByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(envs)
}

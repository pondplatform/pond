package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/service"
)

type DeploymentHandler struct {
	svc service.DeploymentService
}

func NewDeploymentHandler(svc service.DeploymentService) *DeploymentHandler {
	return &DeploymentHandler{svc: svc}
}

func (h *DeploymentHandler) Submit(w http.ResponseWriter, r *http.Request) {
	var req service.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	d, err := h.svc.Submit(r.Context(), req)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(d)
}

func (h *DeploymentHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid deployment id", http.StatusBadRequest)
		return
	}

	d, err := h.svc.GetStatus(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(d)
}

func (h *DeploymentHandler) ProvideUserInput(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid deployment id", http.StatusBadRequest)
		return
	}

	depName := r.PathValue("name")
	if depName == "" {
		http.Error(w, "missing dependency name", http.StatusBadRequest)
		return
	}

	var req service.UserInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := h.svc.ProvideUserInput(r.Context(), id, depName, req); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

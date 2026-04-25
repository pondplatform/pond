package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/service"
	"github.com/pondplatform/pond/internal/server/store"
)

type DeploymentHandler struct {
	svc      service.DeploymentService
	services store.ServiceRepository
}

func NewDeploymentHandler(svc service.DeploymentService, services store.ServiceRepository) *DeploymentHandler {
	return &DeploymentHandler{svc: svc, services: services}
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

// deploymentListItem is a reduced view of a deployment for list responses.
type deploymentListItem struct {
	ID            uuid.UUID  `json:"id"`
	ServiceID     uuid.UUID  `json:"serviceId"`
	EnvironmentID uuid.UUID  `json:"environmentId"`
	ImageTag      string     `json:"imageTag"`
	Status        string     `json:"status"`
	TriggeredBy   string     `json:"triggeredBy"`
	CreatedAt     time.Time  `json:"createdAt"`
	CompletedAt   *time.Time `json:"completedAt"`
}

func (h *DeploymentHandler) ListByService(w http.ResponseWriter, r *http.Request) {
	serviceID, err := uuid.Parse(r.PathValue("serviceId"))
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}

	// Verify service exists
	if _, err := h.services.GetByID(r.Context(), serviceID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "service not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	p := ParsePagination(r)

	// Parse optional filters
	var environmentID *uuid.UUID
	if envStr := r.URL.Query().Get("environment_id"); envStr != "" {
		id, err := uuid.Parse(envStr)
		if err != nil {
			http.Error(w, "invalid environment_id", http.StatusBadRequest)
			return
		}
		environmentID = &id
	}

	var status *domain.DeploymentStatus
	if statusStr := r.URL.Query().Get("status"); statusStr != "" {
		s := domain.DeploymentStatus(statusStr)
		status = &s
	}

	deployments, err := h.svc.ListByService(r.Context(), serviceID, environmentID, status, p.Limit, p.Cursor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var nextCursor *string
	if len(deployments) > p.Limit {
		deployments = deployments[:p.Limit]
		cursor := deployments[len(deployments)-1].CreatedAt.Format(time.RFC3339Nano)
		nextCursor = &cursor
	}

	// Convert to list items (reduced view)
	items := make([]deploymentListItem, len(deployments))
	for i, d := range deployments {
		items[i] = deploymentListItem{
			ID:            d.ID,
			ServiceID:     d.ServiceID,
			EnvironmentID: d.EnvironmentID,
			ImageTag:      d.ImageTag,
			Status:        string(d.Status),
			TriggeredBy:   d.TriggeredBy,
			CreatedAt:     d.CreatedAt,
			CompletedAt:   d.CompletedAt,
		}
	}

	writeList(w, items, nextCursor)
}

func (h *DeploymentHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid deployment id", http.StatusBadRequest)
		return
	}

	if err := h.svc.Cancel(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, domain.ErrInvalidInput) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DeploymentHandler) Validate(w http.ResponseWriter, r *http.Request) {
	var req service.SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	result, err := h.svc.Validate(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

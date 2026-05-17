package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
	shared "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type DeploymentHandler struct {
	svc      service.DeploymentService
	services store.ServiceRepository
	log      *slog.Logger
}

func NewDeploymentHandler(svc service.DeploymentService, services store.ServiceRepository, log *slog.Logger) *DeploymentHandler {
	return &DeploymentHandler{svc: svc, services: services, log: log}
}

func (h *DeploymentHandler) Submit(w http.ResponseWriter, r *http.Request) {
	req := defaultSubmitRequest()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d, err := h.submit(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toDeploymentResponse(d))
}

func (h *DeploymentHandler) submit(ctx context.Context, req shared.SubmitRequest) (*domain.Deployment, error) {
	encoded, _ := json.Marshal(req)
	h.log.Info("Deploying service: " + string(encoded))

	return h.svc.Submit(ctx, service.SubmitRequest{
		ProjectID:         req.ProjectID,
		EnvironmentName:   req.EnvironmentName,
		OverridableConfig: req.OverridableConfig,
		ImageTag:          req.ImageTag,
		TriggeredBy:       req.TriggeredBy,
		CreateIfNotExists: req.CreateIfNotExists,
	})
}

func defaultSubmitRequest() shared.SubmitRequest {
	return shared.SubmitRequest{
		TriggeredBy:       "api",
		CreateIfNotExists: true,
		OverridableConfig: serviceconfig.OverridableConfig{
			ServiceConfig: serviceconfig.ServiceConfig{
				Service: &serviceconfig.ServiceSpec{
					Port:     serviceconfig.Ptr(int32(8080)),
					Replicas: serviceconfig.Ptr(int32(2)),
				},
			},
		},
	}
}

func (h *DeploymentHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	d, err := h.getStatus(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDeploymentResponse(d))
}

func (h *DeploymentHandler) getStatus(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	return h.svc.GetStatus(ctx, id)
}

func toDeploymentResponse(d *domain.Deployment) shared.Deployment {
	return shared.Deployment{
		ID:            d.ID,
		ServiceID:     d.ServiceID,
		EnvironmentID: d.EnvironmentID,
		Status:        d.Status,
		CreatedAt:     d.CreatedAt,
	}
}

func (h *DeploymentHandler) ListByService(w http.ResponseWriter, r *http.Request) {
	serviceID, err := uuid.Parse(r.PathValue("serviceId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid service id")
		return
	}

	var environmentID *uuid.UUID
	if envStr := r.URL.Query().Get("environment_id"); envStr != "" {
		id, err := uuid.Parse(envStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid environment_id")
			return
		}
		environmentID = &id
	}

	var status *domain.DeploymentStatus
	if statusStr := r.URL.Query().Get("status"); statusStr != "" {
		s := domain.DeploymentStatus(statusStr)
		status = &s
	}

	p := ParsePagination(r)
	items, nextCursor, err := h.listByService(r.Context(), serviceID, environmentID, status, p)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeList(w, items, nextCursor)
}

func (h *DeploymentHandler) listByService(ctx context.Context, serviceID uuid.UUID, environmentID *uuid.UUID, status *domain.DeploymentStatus, p Pagination) ([]shared.DeploymentListItem, *string, error) {
	if _, err := h.services.GetByID(ctx, serviceID); err != nil {
		return nil, nil, err
	}

	deployments, err := h.svc.ListByService(ctx, serviceID, environmentID, status, p.Limit, p.Cursor)
	if err != nil {
		return nil, nil, err
	}

	var nextCursor *string
	if len(deployments) > p.Limit {
		deployments = deployments[:p.Limit]
		cursor := deployments[len(deployments)-1].CreatedAt.Format(time.RFC3339Nano)
		nextCursor = &cursor
	}

	items := make([]shared.DeploymentListItem, len(deployments))
	for i, d := range deployments {
		items[i] = shared.DeploymentListItem{
			ID:            d.ID,
			ServiceID:     d.ServiceID,
			EnvironmentID: d.EnvironmentID,
			ImageTag:      d.ImageTag,
			Status:        d.Status,
			TriggeredBy:   d.TriggeredBy,
			CreatedAt:     d.CreatedAt,
			CompletedAt:   d.CompletedAt,
		}
	}
	return items, nextCursor, nil
}

func (h *DeploymentHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	if err := h.cancel(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DeploymentHandler) cancel(ctx context.Context, id uuid.UUID) error {
	err := h.svc.Cancel(ctx, id)
	if errors.Is(err, shared.ErrInvalidInput) {
		return shared.ErrConflict
	}
	return err
}

func (h *DeploymentHandler) ConfigureDeployment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid deployment id")
		return
	}
	var req shared.ConfigureDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.configureDeployment(r.Context(), id, req); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DeploymentHandler) configureDeployment(ctx context.Context, id uuid.UUID, req shared.ConfigureDeploymentRequest) error {
	return h.svc.ConfigureDeployment(ctx, id, req.Dependencies)
}

func (h *DeploymentHandler) GetCommandLogs(w http.ResponseWriter, r *http.Request) {
	commandID, err := uuid.Parse(r.PathValue("commandId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid command id")
		return
	}
	items, err := h.getCommandLogs(r.Context(), commandID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeList(w, items, nil)
}

func (h *DeploymentHandler) getCommandLogs(ctx context.Context, commandID uuid.UUID) ([]shared.CommandLog, error) {
	logs, err := h.svc.GetCommandLogs(ctx, commandID)
	if err != nil {
		return nil, err
	}
	items := make([]shared.CommandLog, len(logs))
	for i, l := range logs {
		items[i] = shared.CommandLog{Line: l.Line, LoggedAt: l.LoggedAt}
	}
	return items, nil
}

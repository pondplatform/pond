package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pondplatform/pond/server/internal/auth"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/service"
	"github.com/pondplatform/pond/server/internal/store"
	shared "github.com/pondplatform/pond/shared/server/api"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

type DeploymentHandler struct {
	svc        service.DeploymentService
	services   store.ServiceRepository
	authorizer auth.Authorizer
	log        *slog.Logger
}

func NewDeploymentHandler(svc service.DeploymentService, services store.ServiceRepository, authorizer auth.Authorizer, log *slog.Logger) *DeploymentHandler {
	return &DeploymentHandler{svc: svc, services: services, authorizer: authorizer, log: log}
}

func (h *DeploymentHandler) Submit(c *gin.Context) {
	req := defaultSubmitRequest()
	if !bindJSON(c, &req) {
		return
	}

	// Project ID comes from the request body, not the path, so the middleware
	// cannot do the ownership check — verify here via the authorizer.
	identity, ok := IdentityFromContext(c.Request.Context())
	if !ok {
		writeInternalError(c, errors.New("no identity in request context"), h.log)
		return
	}
	if err := h.authorizer.Authorize(c.Request.Context(), identity, auth.Action{
		Resource:   auth.ResourceProject,
		Verb:       auth.VerbWrite,
		ResourceID: req.ProjectID,
	}); err != nil {
		writeServiceError(c, err, h.log)
		return
	}

	d, err := h.submit(c.Request.Context(), req)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusCreated, toDeploymentResponse(d))
}

func (h *DeploymentHandler) submit(ctx context.Context, req shared.SubmitRequest) (*domain.Deployment, error) {
	// Dependency values can carry credentials, so they stay out of the log.
	logged := req
	logged.Dependencies = nil
	encoded, _ := json.Marshal(logged)
	h.log.Info("Deploying service: " + string(encoded))

	return h.svc.Submit(ctx, req)
}

func defaultSubmitRequest() shared.SubmitRequest {
	return shared.SubmitRequest{
		TriggeredBy:       "api",
		CreateIfNotExists: true,
		OverridableConfig: serviceconfig.OverridableConfig{
			ServiceConfig: serviceconfig.ServiceConfig{
				Deployment: &serviceconfig.DeploymentSpec{
					Replicas: serviceconfig.Ptr(int32(1)),
				},
			},
		},
	}
}

func (h *DeploymentHandler) GetStatus(c *gin.Context) {
	id, ok := parseUUIDParam(c, "deploymentId", "deployment id")
	if !ok {
		return
	}
	detail, err := h.svc.GetStatus(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusOK, toDeploymentDetailResponse(detail))
}

func toDeploymentDetailResponse(detail *service.DeploymentDetail) shared.Deployment {
	d := detail.Deployment

	deps := make([]shared.DependencyDeploymentSummary, len(detail.Dependencies))
	for i, dep := range detail.Dependencies {
		deps[i] = shared.DependencyDeploymentSummary{
			Name:        dep.DependencyName,
			Type:        dep.DependencyType,
			Managed:     dep.Managed,
			Status:      shared.DependencyDeploymentStatus(dep.Status),
			CommandID:   dep.CommandID,
			CompletedAt: dep.CompletedAt,
		}
	}

	cmds := make([]shared.CommandSummary, len(detail.Commands))
	for i, cmd := range detail.Commands {
		cmds[i] = shared.CommandSummary{
			ID:        cmd.ID,
			Type:      cmd.Type,
			Status:    shared.CommandStatus(cmd.Status),
			Error:     cmd.Error,
			CreatedAt: cmd.CreatedAt,
			UpdatedAt: cmd.UpdatedAt,
		}
	}

	return shared.Deployment{
		ID:            d.ID,
		ServiceID:     d.ServiceID,
		EnvironmentID: d.EnvironmentID,
		ImageTag:      d.ImageTag,
		TriggeredBy:   d.TriggeredBy,
		Status:        d.Status,
		CreatedAt:     d.CreatedAt,
		CompletedAt:   d.CompletedAt,
		Error:         d.Error,
		Dependencies:  deps,
		Commands:      cmds,
	}
}

func toDeploymentResponse(d *domain.Deployment) shared.Deployment {
	return shared.Deployment{
		ID:            d.ID,
		ServiceID:     d.ServiceID,
		EnvironmentID: d.EnvironmentID,
		ImageTag:      d.ImageTag,
		TriggeredBy:   d.TriggeredBy,
		Status:        d.Status,
		CreatedAt:     d.CreatedAt,
		CompletedAt:   d.CompletedAt,
		Error:         d.Error,
		Dependencies:  []shared.DependencyDeploymentSummary{},
		Commands:      []shared.CommandSummary{},
	}
}

func (h *DeploymentHandler) ListByService(c *gin.Context) {
	serviceID, ok := parseUUIDParam(c, "serviceId", "service id")
	if !ok {
		return
	}

	var environmentID *uuid.UUID
	if envStr := c.Query("environment_id"); envStr != "" {
		id, err := uuid.Parse(envStr)
		if err != nil {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("invalid environment_id %q: expected a UUID", envStr))
			return
		}
		environmentID = &id
	}

	var status *domain.DeploymentStatus
	if statusStr := c.Query("status"); statusStr != "" {
		s := domain.DeploymentStatus(statusStr)
		if !slices.Contains(deploymentStatuses, s) {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("invalid status %q: expected one of %s", statusStr, joinStatuses(deploymentStatuses)))
			return
		}
		status = &s
	}

	p := ParsePagination(c.Request)
	if p.Cursor != "" {
		if _, err := time.Parse(time.RFC3339Nano, p.Cursor); err != nil {
			writeError(c, http.StatusBadRequest, fmt.Sprintf("invalid cursor %q: pass the nextCursor value from a previous response", p.Cursor))
			return
		}
	}
	items, nextCursor, err := h.listByService(c.Request.Context(), serviceID, environmentID, status, p)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeList(c, items, nextCursor)
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

func (h *DeploymentHandler) Cancel(c *gin.Context) {
	id, ok := parseUUIDParam(c, "deploymentId", "deployment id")
	if !ok {
		return
	}
	if err := h.cancel(c.Request.Context(), id); err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DeploymentHandler) cancel(ctx context.Context, id uuid.UUID) error {
	return h.svc.Cancel(ctx, id)
}

func (h *DeploymentHandler) ConfigureDeployment(c *gin.Context) {
	id, ok := parseUUIDParam(c, "deploymentId", "deployment id")
	if !ok {
		return
	}
	var req shared.ConfigureDeploymentRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := h.configureDeployment(c.Request.Context(), id, req); err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DeploymentHandler) configureDeployment(ctx context.Context, id uuid.UUID, req shared.ConfigureDeploymentRequest) error {
	return h.svc.ConfigureDeployment(ctx, id, req.Dependencies)
}

func (h *DeploymentHandler) GetCommandLogs(c *gin.Context) {
	commandID, ok := parseUUIDParam(c, "commandId", "command id")
	if !ok {
		return
	}
	items, err := h.getCommandLogs(c.Request.Context(), commandID)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeList(c, items, nil)
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

var deploymentStatuses = []domain.DeploymentStatus{
	shared.DeploymentStatusPending,
	shared.DeploymentStatusRunning,
	shared.DeploymentStatusSucceeded,
	shared.DeploymentStatusAwaitingInput,
	shared.DeploymentStatusFailed,
	shared.DeploymentStatusCancelled,
}

func joinStatuses(statuses []domain.DeploymentStatus) string {
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

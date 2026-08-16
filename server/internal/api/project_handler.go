package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

type ProjectHandler struct {
	projects store.ProjectRepository
	envs     store.EnvironmentRepository
	log      *slog.Logger
}

func NewProjectHandler(projects store.ProjectRepository, envs store.EnvironmentRepository, log *slog.Logger) *ProjectHandler {
	return &ProjectHandler{projects: projects, envs: envs, log: log}
}

func toProjectResponse(p *domain.Project) api.Project {
	return api.Project{
		ID:                p.ID,
		Name:              p.Name,
		RootEnvironmentID: p.RootEnvironmentID,
		CreatedAt:         p.CreatedAt,
	}
}

func (h *ProjectHandler) Create(c *gin.Context) {
	var req api.CreateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	project, err := h.create(c.Request.Context(), req)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusCreated, toProjectResponse(project))
}

func (h *ProjectHandler) create(ctx context.Context, req api.CreateProjectRequest) (*domain.Project, error) {
	req.Name = strings.TrimSpace(req.Name)
	if err := req.Validate(); err != nil {
		return nil, err
	}

	project := &domain.Project{
		ID:        uuid.New(),
		Name:      req.Name,
		CreatedAt: time.Now().UTC(),
	}

	existing, err := h.projects.GetByName(ctx, req.Name)
	if err == nil && existing != nil {
		return nil, api.ErrAlreadyExists
	}
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return nil, err
	}

	if err := h.projects.Create(ctx, project); err != nil {
		return nil, err
	}
	return project, nil
}

func (h *ProjectHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid project id")
		return
	}
	project, err := h.projects.GetByID(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusOK, toProjectResponse(project))
}

func (h *ProjectHandler) List(c *gin.Context) {
	projects, err := h.projects.List(c.Request.Context())
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	items := make([]api.Project, len(projects))
	for i, p := range projects {
		items[i] = toProjectResponse(&p)
	}
	writeList(c, items, nil)
}

func (h *ProjectHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid project id")
		return
	}
	var req api.UpdateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	project, err := h.update(c.Request.Context(), id, req)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusOK, toProjectResponse(project))
}

func (h *ProjectHandler) update(ctx context.Context, id uuid.UUID, req api.UpdateProjectRequest) (*domain.Project, error) {
	project, err := h.projects.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.RootEnvironmentID != nil {
		env, err := h.envs.GetByID(ctx, *req.RootEnvironmentID)
		if err != nil {
			return nil, err
		}
		if env.ProjectID != id {
			return nil, api.ErrInvalidInput
		}

		if err := h.projects.SetRootEnvironment(ctx, id, *req.RootEnvironmentID); err != nil {
			return nil, err
		}
		project.RootEnvironmentID = req.RootEnvironmentID
	}

	return project, nil
}

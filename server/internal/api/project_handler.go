package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

type ProjectHandler struct {
	projects store.ProjectRepository
	orgs     store.OrganizationRepository
	envs     store.EnvironmentRepository
	log      *slog.Logger
}

func NewProjectHandler(projects store.ProjectRepository, orgs store.OrganizationRepository, envs store.EnvironmentRepository, log *slog.Logger) *ProjectHandler {
	return &ProjectHandler{projects: projects, orgs: orgs, envs: envs, log: log}
}

func toProjectResponse(p *domain.Project) api.Project {
	return api.Project{
		ID:                p.ID,
		OrganizationID:    p.OrganizationID,
		Name:              p.Name,
		RootEnvironmentID: p.RootEnvironmentID,
		CreatedAt:         p.CreatedAt,
	}
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	var req api.CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	project, err := h.create(r.Context(), orgID, req)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusCreated, toProjectResponse(project))
}

func (h *ProjectHandler) create(ctx context.Context, orgID uuid.UUID, req api.CreateProjectRequest) (*domain.Project, error) {
	if _, err := h.orgs.GetByID(ctx, orgID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	if err := req.Validate(); err != nil {
		return nil, err
	}

	project := &domain.Project{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Name:           req.Name,
		CreatedAt:      time.Now().UTC(),
	}

	existing, err := h.projects.GetByName(ctx, orgID, req.Name)
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

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	project, err := h.get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, toProjectResponse(project))
}

func (h *ProjectHandler) get(ctx context.Context, id uuid.UUID) (*domain.Project, error) {
	return h.projects.GetByID(ctx, id)
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	items, err := h.list(r.Context(), orgID)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeList(w, items, nil)
}

func (h *ProjectHandler) list(ctx context.Context, orgID uuid.UUID) ([]api.Project, error) {
	if _, err := h.orgs.GetByID(ctx, orgID); err != nil {
		return nil, err
	}

	projects, err := h.projects.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	items := make([]api.Project, len(projects))
	for i, p := range projects {
		items[i] = toProjectResponse(&p)
	}
	return items, nil
}

func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req api.UpdateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	project, err := h.update(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, toProjectResponse(project))
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

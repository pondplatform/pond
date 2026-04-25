package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type ProjectHandler struct {
	projects store.ProjectRepository
	orgs     store.OrganizationRepository
	envs     store.EnvironmentRepository
}

func NewProjectHandler(projects store.ProjectRepository, orgs store.OrganizationRepository, envs store.EnvironmentRepository) *ProjectHandler {
	return &ProjectHandler{projects: projects, orgs: orgs, envs: envs}
}

type createProjectRequest struct {
	Name string `json:"name"`
}

type updateProjectRequest struct {
	RootEnvironmentID *uuid.UUID `json:"rootEnvironmentId"`
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}

	// Verify org exists
	if _, err := h.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organization not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// Check if project already exists
	existing, err := h.projects.GetByName(r.Context(), orgID, req.Name)
	if err == nil && existing != nil {
		writeError(w, http.StatusConflict, "project already exists")
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	project := &domain.Project{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Name:           req.Name,
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.projects.Create(r.Context(), project); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusCreated, project)
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	project, err := h.projects.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, project)
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}

	// Verify org exists
	if _, err := h.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "organization not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	projects, err := h.projects.ListByOrganization(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeList(w, projects, nil)
}

func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	project, err := h.projects.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req updateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RootEnvironmentID != nil {
		// Verify environment exists and belongs to this project
		env, err := h.envs.GetByID(r.Context(), *req.RootEnvironmentID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, "environment not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if env.ProjectID != id {
			writeError(w, http.StatusBadRequest, "environment does not belong to this project")
			return
		}

		if err := h.projects.SetRootEnvironment(r.Context(), id, *req.RootEnvironmentID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		project.RootEnvironmentID = req.RootEnvironmentID
	}

	writeJSON(w, http.StatusOK, project)
}

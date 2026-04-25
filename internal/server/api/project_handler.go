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
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	// Verify org exists
	if _, err := h.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "organization not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Check if project already exists
	existing, err := h.projects.GetByName(r.Context(), orgID, req.Name)
	if err == nil && existing != nil {
		http.Error(w, "project already exists", http.StatusConflict)
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	project := &domain.Project{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Name:           req.Name,
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.projects.Create(r.Context(), project); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, project)
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	project, err := h.projects.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, project)
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	// Verify org exists
	if _, err := h.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "organization not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	projects, err := h.projects.ListByOrganization(r.Context(), orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeList(w, projects, nil)
}

func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	project, err := h.projects.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req updateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.RootEnvironmentID != nil {
		// Verify environment exists and belongs to this project
		env, err := h.envs.GetByID(r.Context(), *req.RootEnvironmentID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "environment not found", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if env.ProjectID != id {
			http.Error(w, "environment does not belong to this project", http.StatusBadRequest)
			return
		}

		if err := h.projects.SetRootEnvironment(r.Context(), id, *req.RootEnvironmentID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		project.RootEnvironmentID = req.RootEnvironmentID
	}

	writeJSON(w, http.StatusOK, project)
}

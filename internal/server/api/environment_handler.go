package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type EnvironmentHandler struct {
	envs     store.EnvironmentRepository
	projects store.ProjectRepository
	clusters store.ClusterRepository
}

func NewEnvironmentHandler(envs store.EnvironmentRepository, projects store.ProjectRepository, clusters store.ClusterRepository) *EnvironmentHandler {
	return &EnvironmentHandler{envs: envs, projects: projects, clusters: clusters}
}

type createEnvironmentRequest struct {
	Name                   string     `json:"name"`
	Namespace              string     `json:"namespace"`
	ClusterID              uuid.UUID  `json:"clusterId"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	DefaultIngressBaseHost string     `json:"defaultIngressBaseHost"`
}

type updateEnvironmentRequest struct {
	ClusterID              *uuid.UUID `json:"clusterId"`
	Namespace              *string    `json:"namespace"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	DefaultIngressBaseHost *string    `json:"defaultIngressBaseHost"`
}

func (h *EnvironmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	// Verify project exists
	project, err := h.projects.GetByID(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req createEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	if req.ClusterID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "cluster_id is required")
		return
	}

	// Verify cluster exists and belongs to same org
	cluster, err := h.clusters.GetByID(r.Context(), req.ClusterID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if cluster.OrganizationID != project.OrganizationID {
		writeError(w, http.StatusBadRequest, "cluster does not belong to the same organization")
		return
	}

	// Check if environment already exists
	existing, err := h.envs.GetByName(r.Context(), projectID, req.Name)
	if err == nil && existing != nil {
		writeError(w, http.StatusConflict, "environment already exists")
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Verify parent environment if provided
	if req.ParentEnvironmentID != nil {
		parent, err := h.envs.GetByID(r.Context(), *req.ParentEnvironmentID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, "parent environment not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if parent.ProjectID != projectID {
			writeError(w, http.StatusBadRequest, "parent environment does not belong to this project")
			return
		}
	}

	env := &domain.Environment{
		ID:                     uuid.New(),
		ProjectID:              projectID,
		ParentEnvironmentID:    req.ParentEnvironmentID,
		Name:                   req.Name,
		Namespace:              req.Namespace,
		DefaultIngressBaseHost: req.DefaultIngressBaseHost,
		ClusterID:              req.ClusterID,
		CreatedAt:              time.Now().UTC(),
	}

	if err := h.envs.Create(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusCreated, env)
}

func (h *EnvironmentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid environment id")
		return
	}

	env, err := h.envs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, env)
}

func (h *EnvironmentHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	// Verify project exists
	if _, err := h.projects.GetByID(r.Context(), projectID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	envs, err := h.envs.ListByProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeList(w, envs, nil)
}

func (h *EnvironmentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid environment id")
		return
	}

	env, err := h.envs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	project, err := h.projects.GetByID(r.Context(), env.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req updateEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ClusterID != nil {
		// Verify cluster exists and belongs to same org
		cluster, err := h.clusters.GetByID(r.Context(), *req.ClusterID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, "cluster not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if cluster.OrganizationID != project.OrganizationID {
			writeError(w, http.StatusBadRequest, "cluster does not belong to the same organization")
			return
		}
		env.ClusterID = *req.ClusterID
	}

	if req.Namespace != nil {
		env.Namespace = *req.Namespace
	}

	if req.DefaultIngressBaseHost != nil {
		env.DefaultIngressBaseHost = *req.DefaultIngressBaseHost
	}

	if req.ParentEnvironmentID != nil {
		// Verify parent environment exists and belongs to same project
		if *req.ParentEnvironmentID != uuid.Nil {
			parent, err := h.envs.GetByID(r.Context(), *req.ParentEnvironmentID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					writeError(w, http.StatusNotFound, "parent environment not found")
					return
				}
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			if parent.ProjectID != env.ProjectID {
				writeError(w, http.StatusBadRequest, "parent environment does not belong to this project")
				return
			}
			// Check for cycle
			if wouldCreateCycle(r.Context(), h.envs, env.ID, *req.ParentEnvironmentID) {
				writeError(w, http.StatusConflict, "setting this parent would create a cycle")
				return
			}
		}
		env.ParentEnvironmentID = req.ParentEnvironmentID
	}

	if err := h.envs.Update(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, env)
}

// wouldCreateCycle checks if setting parentID as the parent of envID would create a cycle.
func wouldCreateCycle(ctx context.Context, envs store.EnvironmentRepository, envID, parentID uuid.UUID) bool {
	visited := make(map[uuid.UUID]bool)
	visited[envID] = true

	current := parentID
	for {
		if visited[current] {
			return true
		}
		visited[current] = true

		env, err := envs.GetByID(ctx, current)
		if err != nil || env.ParentEnvironmentID == nil {
			return false
		}
		current = *env.ParentEnvironmentID
	}
}

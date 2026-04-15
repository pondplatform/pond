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
	ClusterID              uuid.UUID  `json:"cluster_id"`
	ParentEnvironmentID    *uuid.UUID `json:"parent_environment_id"`
	DefaultIngressBaseHost string     `json:"default_ingress_base_host"`
}

type updateEnvironmentRequest struct {
	ClusterID              *uuid.UUID `json:"cluster_id"`
	Namespace              *string    `json:"namespace"`
	ParentEnvironmentID    *uuid.UUID `json:"parent_environment_id"`
	DefaultIngressBaseHost *string    `json:"default_ingress_base_host"`
}

func (h *EnvironmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	// Verify project exists
	project, err := h.projects.GetByID(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req createEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	req.Namespace = strings.TrimSpace(req.Namespace)
	if req.Namespace == "" {
		http.Error(w, "namespace is required", http.StatusBadRequest)
		return
	}
	if req.ClusterID == uuid.Nil {
		http.Error(w, "cluster_id is required", http.StatusBadRequest)
		return
	}

	// Verify cluster exists and belongs to same org
	cluster, err := h.clusters.GetByID(r.Context(), req.ClusterID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if cluster.OrganizationID != project.OrganizationID {
		http.Error(w, "cluster does not belong to the same organization", http.StatusBadRequest)
		return
	}

	// Check if environment already exists
	existing, err := h.envs.GetByName(r.Context(), projectID, req.Name)
	if err == nil && existing != nil {
		http.Error(w, "environment already exists", http.StatusConflict)
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Verify parent environment if provided
	if req.ParentEnvironmentID != nil {
		parent, err := h.envs.GetByID(r.Context(), *req.ParentEnvironmentID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "parent environment not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if parent.ProjectID != projectID {
			http.Error(w, "parent environment does not belong to this project", http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, env)
}

func (h *EnvironmentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid environment id", http.StatusBadRequest)
		return
	}

	env, err := h.envs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, env)
}

func (h *EnvironmentHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		http.Error(w, "invalid project id", http.StatusBadRequest)
		return
	}

	// Verify project exists
	if _, err := h.projects.GetByID(r.Context(), projectID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	envs, err := h.envs.ListByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeList(w, envs, nil)
}

func (h *EnvironmentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid environment id", http.StatusBadRequest)
		return
	}

	env, err := h.envs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	project, err := h.projects.GetByID(r.Context(), env.ProjectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req updateEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ClusterID != nil {
		// Verify cluster exists and belongs to same org
		cluster, err := h.clusters.GetByID(r.Context(), *req.ClusterID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				http.Error(w, "cluster not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cluster.OrganizationID != project.OrganizationID {
			http.Error(w, "cluster does not belong to the same organization", http.StatusBadRequest)
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
					http.Error(w, "parent environment not found", http.StatusNotFound)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if parent.ProjectID != env.ProjectID {
				http.Error(w, "parent environment does not belong to this project", http.StatusBadRequest)
				return
			}
			// Check for cycle
			if wouldCreateCycle(r.Context(), h.envs, env.ID, *req.ParentEnvironmentID) {
				http.Error(w, "setting this parent would create a cycle", http.StatusConflict)
				return
			}
		}
		env.ParentEnvironmentID = req.ParentEnvironmentID
	}

	if err := h.envs.Update(r.Context(), env); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

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

type EnvironmentHandler struct {
	envs     store.EnvironmentRepository
	projects store.ProjectRepository
	clusters store.ClusterRepository
	log      *slog.Logger
}

func NewEnvironmentHandler(envs store.EnvironmentRepository, projects store.ProjectRepository, clusters store.ClusterRepository, log *slog.Logger) *EnvironmentHandler {
	return &EnvironmentHandler{envs: envs, projects: projects, clusters: clusters, log: log}
}

func toEnvironmentResponse(e *domain.Environment) api.Environment {
	return api.Environment{
		ID:                     e.ID,
		ProjectID:              e.ProjectID,
		ParentEnvironmentID:    e.ParentEnvironmentID,
		Name:                   e.Name,
		Namespace:              e.Namespace,
		DefaultIngressBaseHost: e.DefaultIngressBaseHost,
		ClusterID:              e.ClusterID,
		CreatedAt:              e.CreatedAt,
	}
}

func (h *EnvironmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req api.CreateEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	env, err := h.create(r.Context(), projectID, req)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusCreated, toEnvironmentResponse(env))
}

func (h *EnvironmentHandler) create(ctx context.Context, projectID uuid.UUID, req api.CreateEnvironmentRequest) (*domain.Environment, error) {
	if _, err := h.projects.GetByID(ctx, projectID); err != nil {
		return nil, err
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Namespace = strings.TrimSpace(req.Namespace)
	if err := req.Validate(); err != nil {
		return nil, err
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

	if _, err := h.clusters.GetByID(ctx, req.ClusterID); err != nil {
		return nil, err
	}

	existing, err := h.envs.GetByName(ctx, projectID, req.Name)
	if err == nil && existing != nil {
		return nil, api.ErrAlreadyExists
	}
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return nil, err
	}

	if req.ParentEnvironmentID != nil {
		parent, err := h.envs.GetByID(ctx, *req.ParentEnvironmentID)
		if err != nil {
			return nil, err
		}
		if parent.ProjectID != projectID {
			return nil, api.ErrInvalidInput
		}
	}

	if err := h.envs.Create(ctx, env); err != nil {
		return nil, err
	}
	return env, nil
}

func (h *EnvironmentHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("envId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid environment id")
		return
	}
	env, err := h.get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, toEnvironmentResponse(env))
}

func (h *EnvironmentHandler) get(ctx context.Context, id uuid.UUID) (*domain.Environment, error) {
	return h.envs.GetByID(ctx, id)
}

func (h *EnvironmentHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("projectId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	items, err := h.list(r.Context(), projectID)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeList(w, items, nil)
}

func (h *EnvironmentHandler) list(ctx context.Context, projectID uuid.UUID) ([]api.Environment, error) {
	if _, err := h.projects.GetByID(ctx, projectID); err != nil {
		return nil, err
	}

	envs, err := h.envs.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}

	items := make([]api.Environment, len(envs))
	for i, e := range envs {
		items[i] = toEnvironmentResponse(&e)
	}
	return items, nil
}

func (h *EnvironmentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("envId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid environment id")
		return
	}
	var req api.UpdateEnvironmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	env, err := h.update(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, toEnvironmentResponse(env))
}

func (h *EnvironmentHandler) update(ctx context.Context, id uuid.UUID, req api.UpdateEnvironmentRequest) (*domain.Environment, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	env, err := h.envs.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.ClusterID != nil {
		if _, err := h.clusters.GetByID(ctx, *req.ClusterID); err != nil {
			return nil, err
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
		if *req.ParentEnvironmentID != uuid.Nil {
			parent, err := h.envs.GetByID(ctx, *req.ParentEnvironmentID)
			if err != nil {
				return nil, err
			}
			if parent.ProjectID != env.ProjectID {
				return nil, api.ErrInvalidInput
			}
			if wouldCreateCycle(ctx, h.envs, env.ID, *req.ParentEnvironmentID) {
				return nil, api.ErrConflict
			}
		}
		env.ParentEnvironmentID = req.ParentEnvironmentID
	}

	if err := h.envs.Update(ctx, env); err != nil {
		return nil, err
	}
	return env, nil
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

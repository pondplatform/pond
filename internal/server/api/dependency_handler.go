package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type DependencyHandler struct {
	configs  domain.DependencyConfigRepository
	contexts domain.ResolvedContextRepository
}

func NewDependencyHandler(configs domain.DependencyConfigRepository, contexts domain.ResolvedContextRepository) *DependencyHandler {
	return &DependencyHandler{configs: configs, contexts: contexts}
}

func (h *DependencyHandler) SetConfig(w http.ResponseWriter, r *http.Request) {
	serviceID, err := uuid.Parse(r.PathValue("serviceID"))
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		http.Error(w, "invalid environment id", http.StatusBadRequest)
		return
	}
	depName := r.PathValue("name")

	var cfg domain.DependencyConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	cfg.ServiceID = serviceID
	cfg.EnvironmentID = envID
	cfg.DependencyName = depName

	if cfg.ID == uuid.Nil {
		cfg.ID = uuid.New()
	}

	if err := h.configs.Set(r.Context(), &cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cfg)
}

func (h *DependencyHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	serviceID, err := uuid.Parse(r.PathValue("serviceID"))
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		http.Error(w, "invalid environment id", http.StatusBadRequest)
		return
	}
	depName := r.PathValue("name")

	cfg, err := h.configs.Get(r.Context(), serviceID, envID, depName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(cfg)
}

func (h *DependencyHandler) GetContext(w http.ResponseWriter, r *http.Request) {
	serviceID, err := uuid.Parse(r.PathValue("serviceID"))
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}
	envID, err := uuid.Parse(r.PathValue("envID"))
	if err != nil {
		http.Error(w, "invalid environment id", http.StatusBadRequest)
		return
	}
	depName := r.PathValue("name")

	rc, err := h.contexts.Get(r.Context(), serviceID, envID, depName)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(rc)
}

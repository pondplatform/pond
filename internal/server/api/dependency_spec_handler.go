package api

import (
	"errors"
	"net/http"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
)

type DependencySpecHandler struct {
	registry dependency.SpecRegistry
}

func NewDependencySpecHandler(registry dependency.SpecRegistry) *DependencySpecHandler {
	return &DependencySpecHandler{registry: registry}
}

func (h *DependencySpecHandler) List(w http.ResponseWriter, r *http.Request) {
	specs := h.registry.List()
	writeList(w, specs, nil)
}

func (h *DependencySpecHandler) Get(w http.ResponseWriter, r *http.Request) {
	depType := r.PathValue("type")
	if depType == "" {
		http.Error(w, "missing type", http.StatusBadRequest)
		return
	}

	spec, err := h.registry.Get(depType)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, spec)
}

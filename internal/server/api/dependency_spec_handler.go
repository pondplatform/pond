package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/dependency"
)

type DependencySpecHandler struct {
	registry dependency.SpecRegistry
	log      *slog.Logger
}

func NewDependencySpecHandler(registry dependency.SpecRegistry, log *slog.Logger) *DependencySpecHandler {
	return &DependencySpecHandler{registry: registry, log: log}
}

func (h *DependencySpecHandler) List(w http.ResponseWriter, r *http.Request) {
	specs := h.registry.List()
	writeList(w, specs, nil)
}

func (h *DependencySpecHandler) Get(w http.ResponseWriter, r *http.Request) {
	depType := r.PathValue("type")
	if depType == "" {
		writeError(w, http.StatusBadRequest, "missing type")
		return
	}

	spec, err := h.registry.Get(depType)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, spec)
}

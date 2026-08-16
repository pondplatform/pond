package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/dependency"
	"github.com/pondplatform/pond/shared/server/api"
)

type DependencySpecHandler struct {
	registry dependency.SpecRegistry
	log      *slog.Logger
}

func NewDependencySpecHandler(registry dependency.SpecRegistry, log *slog.Logger) *DependencySpecHandler {
	return &DependencySpecHandler{registry: registry, log: log}
}

func toDependencySpecResponse(d domain.DependencySpec) api.DependencySpec {
	configFields := make([]api.FieldSpec, len(d.ConfigFields))
	for i, f := range d.ConfigFields {
		configFields[i] = api.FieldSpec{
			Name:        f.Name,
			Description: f.Description,
			Required:    f.Required,
			Sensitive:   f.Sensitive,
		}
	}
	outputFields := make([]api.FieldSpec, len(d.OutputFields))
	for i, f := range d.OutputFields {
		outputFields[i] = api.FieldSpec{
			Name:        f.Name,
			Description: f.Description,
			Required:    f.Required,
			Sensitive:   f.Sensitive,
		}
	}
	return api.DependencySpec{
		Type:         d.Type,
		Description:  d.Description,
		ConfigFields: configFields,
		OutputFields: outputFields,
	}
}

func (h *DependencySpecHandler) List(c *gin.Context) {
	items := h.list()
	writeList(c, items, nil)
}

func (h *DependencySpecHandler) list() []api.DependencySpec {
	specs := h.registry.List()
	items := make([]api.DependencySpec, len(specs))
	for i, s := range specs {
		items[i] = toDependencySpecResponse(s)
	}
	return items
}

func (h *DependencySpecHandler) Get(c *gin.Context) {
	depType := c.Param("type")
	if depType == "" {
		writeError(c, http.StatusBadRequest, "missing type")
		return
	}
	spec, err := h.get(c.Request.Context(), depType)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusOK, toDependencySpecResponse(spec))
}

func (h *DependencySpecHandler) get(_ context.Context, depType string) (domain.DependencySpec, error) {
	return h.registry.Get(depType)
}

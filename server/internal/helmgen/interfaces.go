package helmgen

import (
	"github.com/pondplatform/pond/shared/serviceconfig"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// HelmValuesGenerator produces the Helm values YAML from a resolved
// service config and its dependency contexts (dependency name → output values).
type HelmValuesGenerator interface {
	Generate(cfg *serviceconfig.ServiceConfig, env *domain.Environment, contexts map[string]map[string]any) (*HelmValues, error)
}

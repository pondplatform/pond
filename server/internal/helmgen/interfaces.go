package helmgen

import (
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// HelmValuesGenerator produces the Helm values YAML from a resolved
// service config and its dependency contexts (dependency name → output values).
type HelmValuesGenerator interface {
	Generate(cfg *serviceconfig.ServiceConfig, env *domain.Environment) (*HelmValues, error)
}

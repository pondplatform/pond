package helmgen

import (
	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// HelmValuesGenerator produces the Helm values YAML from a resolved
// service config and its dependency contexts (dependency name → output values).
type HelmValuesGenerator interface {
	Generate(cfg *serviceconfig.ServiceConfig, env *domain.Environment, imageTag string, deploymentID uuid.UUID) (*HelmValues, error)
}

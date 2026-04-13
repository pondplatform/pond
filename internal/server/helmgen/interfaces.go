package helmgen

import "github.com/pondplatform/pond/internal/common/domain"

// HelmValuesGenerator produces the Helm values YAML from a resolved
// service config and its dependency contexts (dependency name → output values).
type HelmValuesGenerator interface {
	Generate(cfg *domain.ServiceConfig, env *domain.Environment, contexts map[string]map[string]any) (*HelmValues, error)
}

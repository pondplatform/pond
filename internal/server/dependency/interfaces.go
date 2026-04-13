package dependency

import "github.com/pondplatform/pond/internal/common/domain"

// SpecRegistry is a read-only registry of built-in dependency type schemas.
type SpecRegistry interface {
	Get(depType string) (domain.DependencySpec, error)
	List() []domain.DependencySpec
	Exists(depType string) bool
}

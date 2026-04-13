package domain

import (
	"time"

	"github.com/google/uuid"
)

// DependencyDeclaration represents a dependency as declared in pond.yml.
type DependencyDeclaration struct {
	Type   string         `json:"type"   yaml:"type"`
	Config map[string]any `json:"config" yaml:"config"`
}

// DependencyConfig represents the server-side wiring of a dependency
// for a specific (service, environment) pair.
type DependencyConfig struct {
	ID             uuid.UUID
	ServiceID      uuid.UUID
	EnvironmentID  uuid.UUID
	DependencyName string
	DependencyType string
	Managed        bool
	ProviderInputs map[string]any
	UserConfig     map[string]any
	UpdatedAt      time.Time
}

// DependencySpec describes a built-in dependency type's schema.
type DependencySpec struct {
	Type         string
	Description  string
	ConfigFields []FieldSpec
	OutputFields []FieldSpec
}

type FieldSpec struct {
	Name        string
	Description string
	Required    bool
	Sensitive   bool
}



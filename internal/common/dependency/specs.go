package dependency

import "github.com/pondplatform/pond/internal/common/domain"

func registerBuiltinSpecs(r *specRegistry) {
	r.register(domain.DependencySpec{
		Type:        "postgres",
		Description: "PostgreSQL database",
		ConfigFields: []domain.FieldSpec{
			{Name: "version", Description: "PostgreSQL major version", Required: false},
		},
		OutputFields: []domain.FieldSpec{
			{Name: "host", Description: "Database host", Required: true},
			{Name: "port", Description: "Database port", Required: true},
			{Name: "username", Description: "Database username", Required: true},
			{Name: "password", Description: "Database password", Required: true, Sensitive: true},
			{Name: "database", Description: "Database name", Required: true},
		},
	})

	r.register(domain.DependencySpec{
		Type:        "http-service",
		Description: "External HTTP service (non-managed)",
		ConfigFields: []domain.FieldSpec{
			{Name: "url", Description: "Service URL", Required: true},
		},
		OutputFields: []domain.FieldSpec{
			{Name: "url", Description: "Service URL", Required: true},
		},
	})
}

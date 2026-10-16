package dependency

import (
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

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

	r.register(domain.DependencySpec{
		Type:        serviceconfig.DependencyTypeService,
		Description: "Another Pond service of the same project, in the same environment",
		ConfigFields: []domain.FieldSpec{
			{Name: "service", Description: "Name of the service to depend on", Required: true},
		},
		OutputFields: []domain.FieldSpec{
			{Name: "host", Description: "In-cluster DNS name of the service", Required: true},
			{Name: "port", Description: "Port of the service", Required: true},
			{Name: "url", Description: "In-cluster http URL of the service", Required: true},
			{Name: "gateway.hostname", Description: "Hostname of the service's gateway route, if it has one", Required: false},
			{Name: "gateway.port", Description: "Port the environment's gateway listens on, if the service has a route", Required: false},
			{Name: "gateway.url", Description: "URL of the service through the gateway, if it has a route", Required: false},
		},
	})
}

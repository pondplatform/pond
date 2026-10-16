package service

import (
	"fmt"

	"github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/serviceconfig"
)

// defaultServicePort is the port the base-service chart gives a service that
// sets none.
const defaultServicePort = 80

// importedVariablePrefix starts the export key of a public variable.
const importedVariablePrefix = "variables."

// computeExports are the values a service that depends on cfg can import as
// {{dependencies.<name>.<key>}}: its addresses and its public variables. cfg
// must be rendered, so that none of them carries a template of cfg's own.
func computeExports(cfg *serviceconfig.ServiceConfig, env *db.Environment, variables map[string]string) map[string]string {
	port := int32(defaultServicePort)
	if cfg.Service != nil && cfg.Service.Port != nil {
		port = *cfg.Service.Port
	}
	host := fmt.Sprintf("%s.%s.svc.cluster.local", cfg.Name, env.Namespace)
	exports := map[string]string{
		"host": host,
		"port": fmt.Sprintf("%d", port),
		"url":  fmt.Sprintf("http://%s:%d", host, port),
	}
	for key, value := range gatewayValues(cfg, env) {
		exports["gateway."+key] = value
	}
	for name, declaration := range cfg.Variables {
		if value, given := variables[name]; given && declaration.Public() {
			exports[importedVariablePrefix+name] = value
		}
	}
	return exports
}

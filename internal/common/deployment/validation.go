package deployment

import "github.com/pondplatform/pond/internal/common/domain"

// ValidateServiceConfig performs pre-deploy validation on a ServiceConfig.
func ValidateServiceConfig(cfg *domain.ServiceConfig) *domain.ValidationErrors {
	var errs domain.ValidationErrors

	if cfg.Name == "" {
		errs.Add("config", "name", "service name is required")
	}
	if cfg.Image == "" {
		errs.Add("config", "image", "image is required")
	}
	if cfg.Service.Port <= 0 {
		errs.Add("config", "service.port", "port must be positive")
	}
	if cfg.Service.Replicas <= 0 {
		errs.Add("config", "service.replicas", "replicas must be positive")
	}

	if errs.HasErrors() {
		return &errs
	}
	return nil
}

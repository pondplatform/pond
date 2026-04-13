package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type DependencyConfigStore struct {
	db DBTX
}

func NewDependencyConfigStore(db DBTX) *DependencyConfigStore {
	return &DependencyConfigStore{db: db}
}

func (s *DependencyConfigStore) Get(ctx context.Context, serviceID, envID uuid.UUID, depName string) (*domain.DependencyConfig, error) {
	var cfg domain.DependencyConfig
	var providerInputs, userConfig []byte
	err := s.db.QueryRowContext(ctx,
		"SELECT id, service_id, environment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, updated_at FROM dependency_configs WHERE service_id = $1 AND environment_id = $2 AND dependency_name = $3",
		serviceID, envID, depName,
	).Scan(&cfg.ID, &cfg.ServiceID, &cfg.EnvironmentID, &cfg.DependencyName, &cfg.DependencyType, &cfg.Managed, &providerInputs, &userConfig, &cfg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("dependency config %q: %w", depName, domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get dependency config: %w", err)
	}
	if providerInputs != nil {
		if err := json.Unmarshal(providerInputs, &cfg.ProviderInputs); err != nil {
			return nil, fmt.Errorf("unmarshal provider inputs: %w", err)
		}
	}
	if userConfig != nil {
		if err := json.Unmarshal(userConfig, &cfg.UserConfig); err != nil {
			return nil, fmt.Errorf("unmarshal user config: %w", err)
		}
	}
	return &cfg, nil
}

func (s *DependencyConfigStore) Set(ctx context.Context, cfg *domain.DependencyConfig) error {
	providerInputs, err := json.Marshal(cfg.ProviderInputs)
	if err != nil {
		return fmt.Errorf("marshal provider inputs: %w", err)
	}
	userConfig, err := json.Marshal(cfg.UserConfig)
	if err != nil {
		return fmt.Errorf("marshal user config: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dependency_configs (id, service_id, environment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (service_id, environment_id, dependency_name) DO UPDATE SET
		   dependency_type = EXCLUDED.dependency_type,
		   managed = EXCLUDED.managed,
		   provider_inputs = EXCLUDED.provider_inputs,
		   user_config = EXCLUDED.user_config,
		   updated_at = EXCLUDED.updated_at`,
		cfg.ID, cfg.ServiceID, cfg.EnvironmentID, cfg.DependencyName, cfg.DependencyType, cfg.Managed, providerInputs, userConfig, cfg.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("set dependency config: %w", err)
	}
	return nil
}

func (s *DependencyConfigStore) ListByServiceAndEnv(ctx context.Context, serviceID, envID uuid.UUID) ([]domain.DependencyConfig, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, service_id, environment_id, dependency_name, dependency_type, managed, provider_inputs, user_config, updated_at FROM dependency_configs WHERE service_id = $1 AND environment_id = $2",
		serviceID, envID,
	)
	if err != nil {
		return nil, fmt.Errorf("list dependency configs: %w", err)
	}
	defer rows.Close()

	var configs []domain.DependencyConfig
	for rows.Next() {
		var cfg domain.DependencyConfig
		var providerInputs, userConfig []byte
		if err := rows.Scan(&cfg.ID, &cfg.ServiceID, &cfg.EnvironmentID, &cfg.DependencyName, &cfg.DependencyType, &cfg.Managed, &providerInputs, &userConfig, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan dependency config: %w", err)
		}
		if providerInputs != nil {
			if err := json.Unmarshal(providerInputs, &cfg.ProviderInputs); err != nil {
				return nil, fmt.Errorf("unmarshal provider inputs: %w", err)
			}
		}
		if userConfig != nil {
			if err := json.Unmarshal(userConfig, &cfg.UserConfig); err != nil {
				return nil, fmt.Errorf("unmarshal user config: %w", err)
			}
		}
		configs = append(configs, cfg)
	}
	return configs, rows.Err()
}


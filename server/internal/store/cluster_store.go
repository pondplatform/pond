package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type ClusterStore struct {
	db DBTX
}

func NewClusterStore(db DBTX) *ClusterStore {
	return &ClusterStore{db: db}
}

func (s *ClusterStore) GetByID(ctx context.Context, id uuid.UUID) (*domain.Cluster, error) {
	var c domain.Cluster
	var lastSeen sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, agent_token_hash, last_seen_at, created_at FROM clusters WHERE id = $1", id,
	).Scan(&c.ID, &c.Name, &c.AgentTokenHash, &lastSeen, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("cluster %s: %w", id, api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get cluster: %w", err)
	}
	if lastSeen.Valid {
		c.LastSeenAt = &lastSeen.Time
	}
	return &c, nil
}

func (s *ClusterStore) GetByName(ctx context.Context, name string) (*domain.Cluster, error) {
	var c domain.Cluster
	var lastSeen sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, agent_token_hash, last_seen_at, created_at FROM clusters WHERE name = $1",
		name,
	).Scan(&c.ID, &c.Name, &c.AgentTokenHash, &lastSeen, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("cluster %q: %w", name, api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get cluster: %w", err)
	}
	if lastSeen.Valid {
		c.LastSeenAt = &lastSeen.Time
	}
	return &c, nil
}

func (s *ClusterStore) List(ctx context.Context) ([]domain.Cluster, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, name, agent_token_hash, last_seen_at, created_at FROM clusters",
	)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	defer rows.Close()

	var clusters []domain.Cluster
	for rows.Next() {
		var c domain.Cluster
		var lastSeen sql.NullTime
		if err := rows.Scan(&c.ID, &c.Name, &c.AgentTokenHash, &lastSeen, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan cluster: %w", err)
		}
		if lastSeen.Valid {
			c.LastSeenAt = &lastSeen.Time
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

func (s *ClusterStore) Create(ctx context.Context, cluster *domain.Cluster) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO clusters (id, name, agent_token_hash, last_seen_at, created_at) VALUES ($1, $2, $3, $4, $5)",
		cluster.ID, cluster.Name, cluster.AgentTokenHash, cluster.LastSeenAt, cluster.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create cluster: %w", err)
	}
	return nil
}

func (s *ClusterStore) UpdateLastSeen(ctx context.Context, id uuid.UUID, t time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE clusters SET last_seen_at = $1 WHERE id = $2", t, id,
	)
	if err != nil {
		return fmt.Errorf("update last seen: %w", err)
	}
	return nil
}

func (s *ClusterStore) UpdateTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE clusters SET agent_token_hash = $1 WHERE id = $2", tokenHash, id,
	)
	if err != nil {
		return fmt.Errorf("update token hash: %w", err)
	}
	return nil
}

func (s *ClusterStore) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Cluster, error) {
	var c domain.Cluster
	var lastSeen sql.NullTime
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, agent_token_hash, last_seen_at, created_at FROM clusters WHERE agent_token_hash = $1", tokenHash,
	).Scan(&c.ID, &c.Name, &c.AgentTokenHash, &lastSeen, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("cluster with token: %w", api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get cluster by token: %w", err)
	}
	if lastSeen.Valid {
		c.LastSeenAt = &lastSeen.Time
	}
	return &c, nil
}

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type APITokenStore struct {
	db DBTX
}

func NewAPITokenStore(db DBTX) *APITokenStore {
	return &APITokenStore{db: db}
}

func (s *APITokenStore) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.APIToken, error) {
	var t domain.APIToken
	var lastUsed, revokedAt sql.NullTime
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, organization_id, token_hash, role, description, created_at, last_used_at, revoked_at
		 FROM api_tokens
		 WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash,
	).Scan(&t.ID, &t.OrganizationID, &t.TokenHash, &role, &t.Description, &t.CreatedAt, &lastUsed, &revokedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("api token: %w", domain.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get api token by hash: %w", err)
	}
	t.Role = domain.OrgRole(role)
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	return &t, nil
}

func (s *APITokenStore) Create(ctx context.Context, token *domain.APIToken) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, organization_id, token_hash, role, description, created_at, last_used_at, revoked_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		token.ID, token.OrganizationID, token.TokenHash, string(token.Role), token.Description, token.CreatedAt, token.LastUsedAt, token.RevokedAt,
	)
	if err != nil {
		return fmt.Errorf("create api token: %w", err)
	}
	return nil
}

func (s *APITokenStore) ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]domain.APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, organization_id, token_hash, role, description, created_at, last_used_at, revoked_at
		 FROM api_tokens
		 WHERE organization_id = $1
		 ORDER BY created_at DESC`, orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()

	var tokens []domain.APIToken
	for rows.Next() {
		var t domain.APIToken
		var lastUsed, revokedAt sql.NullTime
		var role string
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.TokenHash, &role, &t.Description, &t.CreatedAt, &lastUsed, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		t.Role = domain.OrgRole(role)
		if lastUsed.Valid {
			t.LastUsedAt = &lastUsed.Time
		}
		if revokedAt.Valid {
			t.RevokedAt = &revokedAt.Time
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (s *APITokenStore) Revoke(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL", id,
	)
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("api token %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

func (s *APITokenStore) UpdateLastUsed(ctx context.Context, id uuid.UUID, t time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE api_tokens SET last_used_at = $1 WHERE id = $2", t, id,
	)
	if err != nil {
		return fmt.Errorf("update api token last used: %w", err)
	}
	return nil
}

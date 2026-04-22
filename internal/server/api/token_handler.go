package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type TokenHandler struct {
	tokens store.APITokenRepository
	orgs   store.OrganizationRepository
}

func NewTokenHandler(tokens store.APITokenRepository, orgs store.OrganizationRepository) *TokenHandler {
	return &TokenHandler{tokens: tokens, orgs: orgs}
}

type createTokenRequest struct {
	Role        string `json:"role"`
	Description string `json:"description"`
}

// tokenResponse is the response model for token endpoints.
// It omits the token hash and optionally includes the plaintext token (only at creation).
type tokenResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Role           string     `json:"role"`
	Description    string     `json:"description"`
	Token          string     `json:"token,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

func tokenToResponse(t *domain.APIToken, plaintext string) tokenResponse {
	return tokenResponse{
		ID:             t.ID,
		OrganizationID: t.OrganizationID,
		Role:           string(t.Role),
		Description:    t.Description,
		Token:          plaintext,
		CreatedAt:      t.CreatedAt,
		LastUsedAt:     t.LastUsedAt,
		RevokedAt:      t.RevokedAt,
	}
}

func (h *TokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	// Verify org exists
	if _, err := h.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "organization not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Validate role
	role := domain.OrgRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		http.Error(w, "role must be admin, member, or viewer", http.StatusBadRequest)
		return
	}

	// Generate token
	plaintext, tokenHash, err := generateToken()
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	token := &domain.APIToken{
		ID:             uuid.New(),
		OrganizationID: orgID,
		TokenHash:      tokenHash,
		Role:           role,
		Description:    strings.TrimSpace(req.Description),
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.tokens.Create(r.Context(), token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, tokenToResponse(token, plaintext))
}

func (h *TokenHandler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	// Verify org exists
	if _, err := h.orgs.GetByID(r.Context(), orgID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "organization not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tokens, err := h.tokens.ListByOrganization(r.Context(), orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]tokenResponse, len(tokens))
	for i, t := range tokens {
		items[i] = tokenToResponse(&t, "")
	}

	writeList(w, items, nil)
}

func (h *TokenHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	tokenID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}

	// List tokens to verify the token belongs to this org
	// (We don't have a GetByID that returns revoked tokens, so we list and filter)
	tokens, err := h.tokens.ListByOrganization(r.Context(), orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	found := false
	for _, t := range tokens {
		if t.ID == tokenID {
			found = true
			if t.RevokedAt != nil {
				http.Error(w, "token already revoked", http.StatusConflict)
				return
			}
			break
		}
	}

	if !found {
		http.Error(w, "token not found", http.StatusNotFound)
		return
	}

	if err := h.tokens.Revoke(r.Context(), tokenID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "token not found or already revoked", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

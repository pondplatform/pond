package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

type TokenHandler struct {
	jwtSecret []byte
}

func NewTokenHandler(jwtSecret []byte) *TokenHandler {
	return &TokenHandler{jwtSecret: jwtSecret}
}

type createTokenRequest struct {
	Role        string `json:"role"`
	Description string `json:"description"`
}

type tokenResponse struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	Role           string    `json:"role"`
	Description    string    `json:"description"`
	Token          string    `json:"token,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func (h *TokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	role := domain.OrgRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		http.Error(w, "role must be admin, member, or viewer", http.StatusBadRequest)
		return
	}

	description := strings.TrimSpace(req.Description)
	now := time.Now().UTC()

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"org_id":      orgID.String(),
		"role":        string(role),
		"description": description,
	})
	signed, err := tok.SignedString(h.jwtSecret)
	if err != nil {
		http.Error(w, "failed to sign token", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, tokenResponse{
		OrganizationID: orgID,
		Role:           string(role),
		Description:    description,
		Token:          signed,
		CreatedAt:      now,
	})
}

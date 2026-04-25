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
	OrganizationID uuid.UUID `json:"organizationId"`
	Role           string    `json:"role"`
	Description    string    `json:"description"`
	Token          string    `json:"token,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

func (h *TokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role := domain.OrgRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		writeError(w, http.StatusBadRequest, "role must be admin, member, or viewer")
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
		writeError(w, http.StatusInternalServerError, "failed to sign token")
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

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

type TokenHandler struct {
	jwtSecret []byte
	log       *slog.Logger
}

func NewTokenHandler(jwtSecret []byte, log *slog.Logger) *TokenHandler {
	return &TokenHandler{jwtSecret: jwtSecret, log: log}
}

func (h *TokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	var req api.CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := h.create(r.Context(), orgID, req)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *TokenHandler) create(_ context.Context, orgID uuid.UUID, req api.CreateTokenRequest) (api.CreateTokenResponse, error) {
	role := domain.OrgRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		return api.CreateTokenResponse{}, api.ErrInvalidInput
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
		return api.CreateTokenResponse{}, err
	}

	return api.CreateTokenResponse{
		OrganizationID: orgID,
		Role:           string(role),
		Description:    description,
		Token:          signed,
		CreatedAt:      now,
	}, nil
}

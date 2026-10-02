package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
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

func (h *TokenHandler) Create(c *gin.Context) {
	var req api.CreateTokenRequest
	if !bindJSON(c, &req) {
		return
	}
	resp, err := h.create(c.Request.Context(), req)
	if err != nil {
		writeServiceError(c, err, h.log)
		return
	}
	writeJSON(c, http.StatusCreated, resp)
}

func (h *TokenHandler) create(_ context.Context, req api.CreateTokenRequest) (api.CreateTokenResponse, error) {
	role := domain.OrgRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != domain.RoleAdmin && role != domain.RoleMember && role != domain.RoleViewer {
		var errs api.ValidationErrors
		errs.Add("Token", "role", fmt.Sprintf("must be one of admin, member or viewer, got %q", req.Role))
		return api.CreateTokenResponse{}, &errs
	}

	description := strings.TrimSpace(req.Description)
	now := time.Now().UTC()

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role":        string(role),
		"description": description,
	})
	signed, err := tok.SignedString(h.jwtSecret)
	if err != nil {
		return api.CreateTokenResponse{}, err
	}

	return api.CreateTokenResponse{
		Role:        string(role),
		Description: description,
		Token:       signed,
		CreatedAt:   now,
	}, nil
}

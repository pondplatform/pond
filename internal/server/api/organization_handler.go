package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type OrganizationHandler struct {
	orgs store.OrganizationRepository
	log  *slog.Logger
}

func NewOrganizationHandler(orgs store.OrganizationRepository, log *slog.Logger) *OrganizationHandler {
	return &OrganizationHandler{orgs: orgs, log: log}
}

type createOrganizationRequest struct {
	Name string `json:"name"`
}

func (h *OrganizationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)

	org := &domain.Organization{
		ID:        uuid.New(),
		Name:      req.Name,
		CreatedAt: time.Now().UTC(),
	}
	if err := org.Validate(); err != nil {
		writeServiceError(w, err)
		return
	}

	// Check if organization already exists
	existing, err := h.orgs.GetByName(r.Context(), req.Name)
	if err == nil && existing != nil {
		writeError(w, http.StatusConflict, "organization already exists")
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := h.orgs.Create(r.Context(), org); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusCreated, org)
}

func (h *OrganizationHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}

	org, err := h.orgs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, org)
}

func (h *OrganizationHandler) List(w http.ResponseWriter, r *http.Request) {
	p := ParsePagination(r)

	orgs, err := h.orgs.List(r.Context(), p.Limit, p.Cursor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var nextCursor *string
	if len(orgs) > p.Limit {
		orgs = orgs[:p.Limit]
		cursor := orgs[len(orgs)-1].CreatedAt.Format(time.RFC3339Nano)
		nextCursor = &cursor
	}

	writeList(w, orgs, nextCursor)
}

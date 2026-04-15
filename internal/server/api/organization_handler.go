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

type OrganizationHandler struct {
	orgs store.OrganizationRepository
}

func NewOrganizationHandler(orgs store.OrganizationRepository) *OrganizationHandler {
	return &OrganizationHandler{orgs: orgs}
}

type createOrganizationRequest struct {
	Name string `json:"name"`
}

func (h *OrganizationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Check if organization already exists
	existing, err := h.orgs.GetByName(r.Context(), req.Name)
	if err == nil && existing != nil {
		http.Error(w, "organization already exists", http.StatusConflict)
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	org := &domain.Organization{
		ID:        uuid.New(),
		Name:      req.Name,
		CreatedAt: time.Now().UTC(),
	}

	if err := h.orgs.Create(r.Context(), org); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, org)
}

func (h *OrganizationHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	org, err := h.orgs.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, org)
}

func (h *OrganizationHandler) List(w http.ResponseWriter, r *http.Request) {
	p := ParsePagination(r)

	orgs, err := h.orgs.List(r.Context(), p.Limit, p.Cursor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/server/internal/store"
	"github.com/pondplatform/pond/shared/server/api"
)

type OrganizationHandler struct {
	orgs store.OrganizationRepository
	log  *slog.Logger
}

func NewOrganizationHandler(orgs store.OrganizationRepository, log *slog.Logger) *OrganizationHandler {
	return &OrganizationHandler{orgs: orgs, log: log}
}

func toOrganizationResponse(o *domain.Organization) api.Organization {
	return api.Organization{
		ID:        o.ID,
		Name:      o.Name,
		CreatedAt: o.CreatedAt,
	}
}

func (h *OrganizationHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req api.CreateOrganizationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	org, err := h.create(r.Context(), req)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusCreated, toOrganizationResponse(org))
}

func (h *OrganizationHandler) create(ctx context.Context, req api.CreateOrganizationRequest) (*domain.Organization, error) {
	req.Name = strings.TrimSpace(req.Name)
	if err := req.Validate(); err != nil {
		return nil, err
	}

	org := &domain.Organization{
		ID:        uuid.New(),
		Name:      req.Name,
		CreatedAt: time.Now().UTC(),
	}

	existing, err := h.orgs.GetByName(ctx, req.Name)
	if err == nil && existing != nil {
		return nil, api.ErrAlreadyExists
	}
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return nil, err
	}

	if err := h.orgs.Create(ctx, org); err != nil {
		return nil, err
	}
	return org, nil
}

func (h *OrganizationHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	org, err := h.get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeJSON(w, http.StatusOK, toOrganizationResponse(org))
}

func (h *OrganizationHandler) get(ctx context.Context, id uuid.UUID) (*domain.Organization, error) {
	return h.orgs.GetByID(ctx, id)
}

func (h *OrganizationHandler) List(w http.ResponseWriter, r *http.Request) {
	p := ParsePagination(r)
	items, nextCursor, err := h.list(r.Context(), p)
	if err != nil {
		writeServiceError(w, err, h.log)
		return
	}
	writeList(w, items, nextCursor)
}

func (h *OrganizationHandler) list(ctx context.Context, p Pagination) ([]api.Organization, *string, error) {
	orgs, err := h.orgs.List(ctx, p.Limit, p.Cursor)
	if err != nil {
		return nil, nil, err
	}

	var nextCursor *string
	if len(orgs) > p.Limit {
		orgs = orgs[:p.Limit]
		cursor := orgs[len(orgs)-1].CreatedAt.Format(time.RFC3339Nano)
		nextCursor = &cursor
	}

	items := make([]api.Organization, len(orgs))
	for i, o := range orgs {
		items[i] = toOrganizationResponse(&o)
	}
	return items, nextCursor, nil
}

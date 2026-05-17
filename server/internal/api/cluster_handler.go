package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

type ClusterHandler struct {
	clusters store.ClusterRepository
	orgs     store.OrganizationRepository
	log      *slog.Logger
}

func NewClusterHandler(clusters store.ClusterRepository, orgs store.OrganizationRepository, log *slog.Logger) *ClusterHandler {
	return &ClusterHandler{clusters: clusters, orgs: orgs, log: log}
}

func clusterToResponse(c *domain.Cluster, token string) api.Cluster {
	return api.Cluster{
		ID:             c.ID,
		OrganizationID: c.OrganizationID,
		Name:           c.Name,
		AgentToken:     token,
		LastSeenAt:     c.LastSeenAt,
		CreatedAt:      c.CreatedAt,
	}
}

func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	var req api.CreateClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cluster, token, err := h.create(r.Context(), orgID, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, clusterToResponse(cluster, token))
}

func (h *ClusterHandler) create(ctx context.Context, orgID uuid.UUID, req api.CreateClusterRequest) (*domain.Cluster, string, error) {
	if _, err := h.orgs.GetByID(ctx, orgID); err != nil {
		return nil, "", err
	}

	req.Name = strings.TrimSpace(req.Name)
	if err := req.Validate(); err != nil {
		return nil, "", err
	}

	token, tokenHash, err := generateToken()
	if err != nil {
		return nil, "", err
	}

	cluster := &domain.Cluster{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Name:           req.Name,
		AgentTokenHash: tokenHash,
		CreatedAt:      time.Now().UTC(),
	}

	existing, err := h.clusters.GetByName(ctx, orgID, req.Name)
	if err == nil && existing != nil {
		return nil, "", api.ErrAlreadyExists
	}
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return nil, "", err
	}

	if err := h.clusters.Create(ctx, cluster); err != nil {
		return nil, "", err
	}
	return cluster, token, nil
}

func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	clusterID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cluster id")
		return
	}
	cluster, err := h.get(r.Context(), orgID, clusterID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clusterToResponse(cluster, ""))
}

func (h *ClusterHandler) get(ctx context.Context, orgID, clusterID uuid.UUID) (*domain.Cluster, error) {
	cluster, err := h.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if cluster.OrganizationID != orgID {
		return nil, api.ErrNotFound
	}
	return cluster, nil
}

func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	items, err := h.list(r.Context(), orgID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeList(w, items, nil)
}

func (h *ClusterHandler) list(ctx context.Context, orgID uuid.UUID) ([]api.Cluster, error) {
	if _, err := h.orgs.GetByID(ctx, orgID); err != nil {
		return nil, err
	}

	clusters, err := h.clusters.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	items := make([]api.Cluster, len(clusters))
	for i, c := range clusters {
		items[i] = clusterToResponse(&c, "")
	}
	return items, nil
}

func (h *ClusterHandler) RotateToken(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid organization id")
		return
	}
	clusterID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cluster id")
		return
	}
	token, err := h.rotateToken(r.Context(), orgID, clusterID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.RotateTokenResponse{AgentToken: token})
}

func (h *ClusterHandler) rotateToken(ctx context.Context, orgID, clusterID uuid.UUID) (string, error) {
	cluster, err := h.clusters.GetByID(ctx, clusterID)
	if err != nil {
		return "", err
	}
	if cluster.OrganizationID != orgID {
		return "", api.ErrNotFound
	}

	token, tokenHash, err := generateToken()
	if err != nil {
		return "", err
	}

	if err := h.clusters.UpdateTokenHash(ctx, clusterID, tokenHash); err != nil {
		return "", err
	}
	return token, nil
}

// generateToken creates a cryptographically random token and its SHA-256 hash.
func generateToken() (plaintext string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = base64.URLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(sum[:])
	return plaintext, hash, nil
}

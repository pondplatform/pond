package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
	"github.com/pondplatform/pond/internal/server/store"
)

type ClusterHandler struct {
	clusters store.ClusterRepository
	orgs     store.OrganizationRepository
}

func NewClusterHandler(clusters store.ClusterRepository, orgs store.OrganizationRepository) *ClusterHandler {
	return &ClusterHandler{clusters: clusters, orgs: orgs}
}

type createClusterRequest struct {
	Name string `json:"name"`
}

// clusterResponse is the response model for cluster endpoints.
// It omits the token hash and optionally includes the plaintext token (only at creation).
type clusterResponse struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Name           string     `json:"name"`
	AgentToken     string     `json:"agent_token,omitempty"`
	LastSeenAt     *time.Time `json:"last_seen_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func clusterToResponse(c *domain.Cluster, token string) clusterResponse {
	return clusterResponse{
		ID:             c.ID,
		OrganizationID: c.OrganizationID,
		Name:           c.Name,
		AgentToken:     token,
		LastSeenAt:     c.LastSeenAt,
		CreatedAt:      c.CreatedAt,
	}
}

type rotateTokenResponse struct {
	AgentToken string `json:"agent_token"`
}

func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
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

	var req createClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Check if cluster already exists
	existing, err := h.clusters.GetByName(r.Context(), orgID, req.Name)
	if err == nil && existing != nil {
		http.Error(w, "cluster already exists", http.StatusConflict)
		return
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Generate token
	token, tokenHash, err := generateToken()
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	cluster := &domain.Cluster{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Name:           req.Name,
		AgentTokenHash: tokenHash,
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.clusters.Create(r.Context(), cluster); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, clusterToResponse(cluster, token))
}

func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	clusterID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid cluster id", http.StatusBadRequest)
		return
	}

	cluster, err := h.clusters.GetByID(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Verify cluster belongs to org
	if cluster.OrganizationID != orgID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, clusterToResponse(cluster, ""))
}

func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
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

	clusters, err := h.clusters.ListByOrganization(r.Context(), orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]clusterResponse, len(clusters))
	for i, c := range clusters {
		items[i] = clusterToResponse(&c, "")
	}

	writeList(w, items, nil)
}

func (h *ClusterHandler) RotateToken(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(r.PathValue("orgId"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}

	clusterID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid cluster id", http.StatusBadRequest)
		return
	}

	cluster, err := h.clusters.GetByID(r.Context(), clusterID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Verify cluster belongs to org
	if cluster.OrganizationID != orgID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Generate new token
	token, tokenHash, err := generateToken()
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	if err := h.clusters.UpdateTokenHash(r.Context(), clusterID, tokenHash); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, rotateTokenResponse{AgentToken: token})
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

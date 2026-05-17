package api

import (
	"time"

	"github.com/google/uuid"
)

type CreateTokenRequest struct {
	Role        string `json:"role"`
	Description string `json:"description"`
}

type CreateTokenResponse struct {
	OrganizationID uuid.UUID `json:"organizationId"`
	Role           string    `json:"role"`
	Description    string    `json:"description"`
	Token          string    `json:"token,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

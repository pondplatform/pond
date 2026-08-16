package api

import "time"

type CreateTokenRequest struct {
	Role        string `json:"role"`
	Description string `json:"description"`
}

type CreateTokenResponse struct {
	Role        string    `json:"role"`
	Description string    `json:"description"`
	Token       string    `json:"token,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

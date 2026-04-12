package domain

import (
	"time"

	"github.com/google/uuid"
)

type Service struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	CreatedAt time.Time
}


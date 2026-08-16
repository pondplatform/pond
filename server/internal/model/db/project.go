package db

import (
	"time"

	"github.com/google/uuid"
)

type Project struct {
	ID                uuid.UUID  `json:"id"`
	Name              string     `json:"name"`
	RootEnvironmentID *uuid.UUID `json:"rootEnvironmentId"`
	CreatedAt         time.Time  `json:"createdAt"`
}

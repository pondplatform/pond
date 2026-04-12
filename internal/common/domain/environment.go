package domain

import (
	"time"

	"github.com/google/uuid"
)

type Environment struct {
	ID                     uuid.UUID
	ProjectID              uuid.UUID
	ParentEnvironmentID    *uuid.UUID
	Name                   string
	Namespace              string
	DefaultIngressBaseHost string
	ClusterID              uuid.UUID
	CreatedAt              time.Time
}


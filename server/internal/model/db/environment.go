package db

import (
	"time"

	"github.com/google/uuid"
)

type Environment struct {
	ID                     uuid.UUID  `json:"id"`
	ProjectID              uuid.UUID  `json:"projectId"`
	ParentEnvironmentID    *uuid.UUID `json:"parentEnvironmentId"`
	Name                   string     `json:"name"`
	Namespace              string     `json:"namespace"`
	DefaultIngressBaseHost string     `json:"defaultIngressBaseHost"`
	GatewayPort            *int32     `json:"gatewayPort"`
	GatewayTLS             bool       `json:"gatewayTls"`
	ClusterID              uuid.UUID  `json:"clusterId"`
	CreatedAt              time.Time  `json:"createdAt"`
}

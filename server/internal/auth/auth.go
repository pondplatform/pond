package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	domain "github.com/pondplatform/pond/server/internal/model/db"
)

// Authenticator resolves a bearer token from a request into an Identity.
// Implementations: TokenAuthenticator (today), OktaAuthenticator (future).
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (*domain.Identity, error)
}

// Authorizer checks whether an identity may perform an action.
type Authorizer interface {
	Authorize(ctx context.Context, identity *domain.Identity, action Action) error
}

// Action represents a permission check combining resource type, verb, and the
// specific resource being accessed (uuid.Nil for collection-level ops).
type Action struct {
	Resource   ResourceType
	Verb       Verb
	ResourceID uuid.UUID
}

// ResourceType identifies the type of resource being accessed.
type ResourceType string

const (
	ResourceCluster     ResourceType = "cluster"
	ResourceProject     ResourceType = "project"
	ResourceEnvironment ResourceType = "environment"
	ResourceService     ResourceType = "service"
	ResourceDeployment  ResourceType = "deployment"
	ResourceCommand     ResourceType = "command"
	ResourceToken       ResourceType = "token"
	ResourceDependency  ResourceType = "dependency"
)

// Verb describes the type of operation being performed.
type Verb string

const (
	VerbRead   Verb = "read"
	VerbWrite  Verb = "write"
	VerbManage Verb = "manage" // admin-only: token CRUD, cluster creation
)

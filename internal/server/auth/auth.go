package auth

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/internal/common/domain"
)

// Authenticator resolves a bearer token from a request into an Identity.
// Implementations: TokenAuthenticator (today), OktaAuthenticator (future).
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (*domain.Identity, error)
}

// Authorizer checks whether an identity may perform an action within an org.
type Authorizer interface {
	Authorize(identity *domain.Identity, action Action, orgID uuid.UUID) error
}

// Action represents a permission check combining resource type and verb.
type Action struct {
	Resource ResourceType
	Verb     Verb
}

// ResourceType identifies the type of resource being accessed.
type ResourceType string

const (
	ResourceOrganization ResourceType = "organization"
	ResourceCluster      ResourceType = "cluster"
	ResourceProject      ResourceType = "project"
	ResourceEnvironment  ResourceType = "environment"
	ResourceService      ResourceType = "service"
	ResourceDeployment   ResourceType = "deployment"
	ResourceToken        ResourceType = "token"
	ResourceDependency   ResourceType = "dependency"
)

// Verb describes the type of operation being performed.
type Verb string

const (
	VerbRead   Verb = "read"
	VerbWrite  Verb = "write"
	VerbManage Verb = "manage" // admin-only: token CRUD, cluster creation, org-level ops
)

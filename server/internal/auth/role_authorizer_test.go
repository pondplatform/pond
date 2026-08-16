package auth

import (
	"context"
	"errors"
	"testing"

	domain "github.com/pondplatform/pond/server/internal/model/db"
	"github.com/pondplatform/pond/shared/server/api"
)

func allow(t *testing.T, identity *domain.Identity, action Action) {
	t.Helper()
	a := NewRoleAuthorizer()
	if err := a.Authorize(context.Background(), identity, action); err != nil {
		t.Errorf("expected allowed, got %v", err)
	}
}

func forbid(t *testing.T, identity *domain.Identity, action Action) {
	t.Helper()
	a := NewRoleAuthorizer()
	err := a.Authorize(context.Background(), identity, action)
	if !errors.Is(err, api.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

// --- admin key ---

func TestRoleAuthorizer_AdminKeyBypassesAll(t *testing.T) {
	identity := &domain.Identity{IsAdminKey: true, Role: domain.RoleViewer}
	// Even a viewer with IsAdminKey=true gets through everything
	allow(t, identity, Action{Resource: ResourceToken, Verb: VerbManage})
}

// --- admin role ---

func TestRoleAuthorizer_AdminAllowsRead(t *testing.T) {
	allow(t, &domain.Identity{Role: domain.RoleAdmin}, Action{Resource: ResourceDeployment, Verb: VerbRead})
}

func TestRoleAuthorizer_AdminAllowsWrite(t *testing.T) {
	allow(t, &domain.Identity{Role: domain.RoleAdmin}, Action{Resource: ResourceProject, Verb: VerbWrite})
}

func TestRoleAuthorizer_AdminAllowsManage(t *testing.T) {
	allow(t, &domain.Identity{Role: domain.RoleAdmin}, Action{Resource: ResourceToken, Verb: VerbManage})
}

// --- member role ---

func TestRoleAuthorizer_MemberAllowsRead(t *testing.T) {
	allow(t, &domain.Identity{Role: domain.RoleMember}, Action{Resource: ResourceDeployment, Verb: VerbRead})
}

func TestRoleAuthorizer_MemberAllowsWrite(t *testing.T) {
	allow(t, &domain.Identity{Role: domain.RoleMember}, Action{Resource: ResourceDeployment, Verb: VerbWrite})
}

func TestRoleAuthorizer_MemberForbiddenOnToken(t *testing.T) {
	forbid(t, &domain.Identity{Role: domain.RoleMember}, Action{Resource: ResourceToken, Verb: VerbRead})
}

func TestRoleAuthorizer_MemberForbiddenOnManage(t *testing.T) {
	forbid(t, &domain.Identity{Role: domain.RoleMember}, Action{Resource: ResourceCluster, Verb: VerbManage})
}

// --- viewer role ---

func TestRoleAuthorizer_ViewerAllowsRead(t *testing.T) {
	allow(t, &domain.Identity{Role: domain.RoleViewer}, Action{Resource: ResourceDeployment, Verb: VerbRead})
}

func TestRoleAuthorizer_ViewerForbiddenOnWrite(t *testing.T) {
	forbid(t, &domain.Identity{Role: domain.RoleViewer}, Action{Resource: ResourceDeployment, Verb: VerbWrite})
}

func TestRoleAuthorizer_ViewerForbiddenOnManage(t *testing.T) {
	forbid(t, &domain.Identity{Role: domain.RoleViewer}, Action{Resource: ResourceCluster, Verb: VerbManage})
}

func TestRoleAuthorizer_ViewerForbiddenOnToken(t *testing.T) {
	forbid(t, &domain.Identity{Role: domain.RoleViewer}, Action{Resource: ResourceToken, Verb: VerbRead})
}

// --- unknown role ---

func TestRoleAuthorizer_UnknownRoleForbidden(t *testing.T) {
	forbid(t, &domain.Identity{Role: "superuser"}, Action{Resource: ResourceDeployment, Verb: VerbRead})
}

// --- resource coverage across multiple types ---

func TestRoleAuthorizer_MemberAllowedOnAllNonTokenResources(t *testing.T) {
	identity := &domain.Identity{Role: domain.RoleMember}
	resources := []ResourceType{
		ResourceCluster, ResourceProject, ResourceEnvironment,
		ResourceService, ResourceDeployment, ResourceCommand, ResourceDependency,
	}
	for _, resource := range resources {
		t.Run(string(resource), func(t *testing.T) {
			allow(t, identity, Action{Resource: resource, Verb: VerbWrite})
		})
	}
}

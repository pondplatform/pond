package db

// OrgRole is the role of an API token.
type OrgRole string

const (
	RoleAdmin  OrgRole = "admin"
	RoleMember OrgRole = "member"
	RoleViewer OrgRole = "viewer"
)

// Identity is the authenticated principal resolved from a JWT bearer token.
type Identity struct {
	Role        OrgRole `json:"role"`
	Description string  `json:"description"`
	// IsAdminKey is true when the request was authenticated via the server-level
	// admin key (POND_ADMIN_KEY). Such identities bypass all authorization checks.
	IsAdminKey bool `json:"isAdminKey"`
}

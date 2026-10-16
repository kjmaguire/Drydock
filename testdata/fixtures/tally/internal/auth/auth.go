// Package auth says who a request is and what they may do.
package auth

import "example.com/tally/internal/store"

// Principal is the member a request acts for: the owner of the API key it presented, in that key's organization, with the
// member's current role.
type Principal struct {
	OrgID  string
	UserID string
	KeyID  string
	Role   store.Role
}

// CanWrite reports whether a role may change anything. Viewers only read.
func CanWrite(r store.Role) bool { return r != store.RoleViewer }

// CanManageTeam reports whether a role may manage the organization's settings, members and invites.
func CanManageTeam(r store.Role) bool { return r == store.RoleOwner || r == store.RoleAdmin }

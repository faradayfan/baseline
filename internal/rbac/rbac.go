// Package rbac implements Baseline's role-based access control: the §7.1 roles,
// the §7.2 permission matrix, and entitlement resolution that walks the
// namespace parent chain (§6).
//
// Two things are modeled separately and must not be conflated:
//
//   - Capability within a namespace where the principal *directly* holds a role
//     — governed by the §7.2 matrix (Role.Can).
//   - Read entitlement, which is inherited transitively UP the parent chain: a
//     member of team:platform-eng can read org (§6). Only reads inherit upward;
//     propose/approve/revoke/manage never do.
package rbac

// Role is a namespace-scoped grant (§7.1). platform_admin is global and handled
// separately (see Principal.PlatformAdmin), not as a namespace Role.
type Role string

const (
	RoleReader      Role = "reader"
	RoleContributor Role = "contributor"
	RoleReviewer    Role = "reviewer"
	RoleAdmin       Role = "namespace_admin"
)

// ValidRole reports whether r is a known namespace role.
func ValidRole(r Role) bool {
	switch r {
	case RoleReader, RoleContributor, RoleReviewer, RoleAdmin:
		return true
	}
	return false
}

// Action is a governed operation in the §7.2 matrix.
type Action string

const (
	ActionReadFacts     Action = "read_facts"
	ActionPropose       Action = "propose_promotion"
	ActionApproveReject Action = "approve_reject"
	ActionRevoke        Action = "revoke_fact"
	ActionManage        Action = "manage_members_policy"
)

// rank orders roles by capability; a higher rank includes every action a lower
// rank can perform (roles are cumulative per §7.1's "+" notation).
var rank = map[Role]int{
	RoleReader:      0,
	RoleContributor: 1,
	RoleReviewer:    2,
	RoleAdmin:       3,
}

// minRole is the least-capable role permitted to perform each action (§7.2).
var minRole = map[Action]Role{
	ActionReadFacts:     RoleReader,
	ActionPropose:       RoleContributor,
	ActionApproveReject: RoleReviewer,
	ActionRevoke:        RoleReviewer,
	ActionManage:        RoleAdmin,
}

// Can reports whether a principal holding role r may perform action a in the
// namespace where r is held. This is the literal §7.2 matrix.
func (r Role) Can(a Action) bool {
	need, ok := minRole[a]
	if !ok {
		return false // unknown action → deny (fail closed)
	}
	have, ok := rank[r]
	if !ok {
		return false // unknown role → deny
	}
	return have >= rank[need]
}

// CanAny reports whether any of the given roles permits the action — used when a
// principal holds several roles in one namespace.
func CanAny(roles []Role, a Action) bool {
	for _, r := range roles {
		if r.Can(a) {
			return true
		}
	}
	return false
}

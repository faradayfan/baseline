package rbac

import "github.com/google/uuid"

// Principal is the resolved identity of a caller (a user or an agent), produced
// by the authn middleware. PlatformAdmin is the global super-role (§7.1) that
// manages the namespace registry and can emergency-override (audited).
type Principal struct {
	ID            string // stable principal identifier (subject)
	PlatformAdmin bool
}

// Grant is one membership row: a role held by a principal in a namespace (§4.4).
type Grant struct {
	NamespaceID uuid.UUID
	Role        Role
}

// Entitlements is a principal's resolved access across namespaces, used by
// handlers to authorize actions and by the context resolver to scope reads.
type Entitlements struct {
	Principal Principal

	// direct maps namespace_id → roles the principal holds *directly* there.
	// Direct roles govern write-ish actions (propose/approve/revoke/manage),
	// which never inherit across namespaces.
	direct map[uuid.UUID][]Role

	// readable is the set of namespace_ids the principal may READ, including
	// ancestors reached transitively up the parent chain (§6).
	readable map[uuid.UUID]struct{}
}

// Can reports whether the principal may perform a write-ish action in a specific
// namespace based on the role(s) held *directly* there. Platform admins pass
// everything (emergency override, §7.1). Reads should use CanRead instead, since
// read entitlement inherits up the parent chain.
func (e Entitlements) Can(ns uuid.UUID, a Action) bool {
	if e.Principal.PlatformAdmin {
		return true
	}
	return CanAny(e.direct[ns], a)
}

// CanRead reports whether the principal may read facts in ns, honoring upward
// inheritance: a role in a descendant namespace entitles reads of its ancestors.
func (e Entitlements) CanRead(ns uuid.UUID) bool {
	if e.Principal.PlatformAdmin {
		return true
	}
	_, ok := e.readable[ns]
	return ok
}

// ReadableNamespaces returns the full set of namespace IDs the principal may
// read, for scoping list/context queries (§6, §10). Order is unspecified.
func (e Entitlements) ReadableNamespaces() []uuid.UUID {
	out := make([]uuid.UUID, 0, len(e.readable))
	for id := range e.readable {
		out = append(out, id)
	}
	return out
}

// RolesIn returns the roles held directly in ns (nil if none).
func (e Entitlements) RolesIn(ns uuid.UUID) []Role { return e.direct[ns] }

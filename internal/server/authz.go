package server

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/faradayfan/baseline/internal/rbac"
)

// authorize checks that the request's principal may perform action a in
// namespace ns, writing a 403 and returning false if not. Read actions honor
// the upward parent-chain inheritance; write actions require a direct role.
// On success it returns the entitlements so the handler can reuse them.
func authorize(w http.ResponseWriter, r *http.Request, ns uuid.UUID, a rbac.Action) (rbac.Entitlements, bool) {
	ent, ok := EntitlementsFrom(r.Context())
	if !ok {
		// Authn middleware must run first; absence is a wiring bug, not a client error.
		writeError(w, http.StatusInternalServerError, "missing entitlements")
		return rbac.Entitlements{}, false
	}

	allowed := ent.Can(ns, a)
	if a == rbac.ActionReadFacts {
		allowed = ent.CanRead(ns)
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return rbac.Entitlements{}, false
	}
	return ent, true
}

// requirePlatformAdmin gates registry-level actions (e.g. creating namespaces)
// to the global platform_admin role (§7.1).
func requirePlatformAdmin(w http.ResponseWriter, r *http.Request) bool {
	p, ok := PrincipalFrom(r.Context())
	if !ok || !p.PlatformAdmin {
		writeError(w, http.StatusForbidden, "platform_admin required")
		return false
	}
	return true
}

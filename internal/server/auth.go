// Package server holds the HTTP layer: authn/authz middleware, handlers, and the
// (later) MCP bridge. It depends on the domain packages, never the reverse.
package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/faradayfan/baseline/internal/rbac"
)

// Authenticator resolves an inbound request to a Principal. Implementations:
// OIDC bearer and mTLS in production (§13); a header-based one for dev/tests.
// Returning ErrUnauthenticated yields 401; any other error yields 500.
type Authenticator interface {
	Authenticate(r *http.Request) (rbac.Principal, error)
}

// ErrUnauthenticated signals a missing/invalid credential (→ 401).
var ErrUnauthenticated = errors.New("unauthenticated")

// ctxKey is unexported to keep request-context keys collision-free.
type ctxKey int

const (
	principalKey ctxKey = iota
	entitlementsKey
)

// resolver loads a principal's entitlements (the rbac.Repo satisfies it).
type resolver interface {
	Resolve(ctx context.Context, p rbac.Principal) (rbac.Entitlements, error)
}

// Authn returns middleware that authenticates the request, resolves the
// principal's entitlements once, and stashes both in the request context for
// downstream handlers. It fails closed: no/invalid credential → 401.
func Authn(auth Authenticator, res resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := auth.Authenticate(r)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					writeError(w, http.StatusUnauthorized, "unauthenticated")
					return
				}
				writeError(w, http.StatusInternalServerError, "auth error")
				return
			}
			ent, err := res.Resolve(r.Context(), p)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "entitlement resolution failed")
				return
			}
			ctx := context.WithValue(r.Context(), principalKey, p)
			ctx = context.WithValue(ctx, entitlementsKey, ent)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// PrincipalFrom returns the authenticated principal from the request context.
func PrincipalFrom(ctx context.Context) (rbac.Principal, bool) {
	p, ok := ctx.Value(principalKey).(rbac.Principal)
	return p, ok
}

// EntitlementsFrom returns the resolved entitlements from the request context.
func EntitlementsFrom(ctx context.Context) (rbac.Entitlements, bool) {
	e, ok := ctx.Value(entitlementsKey).(rbac.Entitlements)
	return e, ok
}

// --- dev authenticator ---

// HeaderAuthenticator resolves identity from request headers. It is for local
// development and tests ONLY — production wires OIDC/mTLS. It reads:
//
//	X-Baseline-Principal: <id>      (required)
//	X-Baseline-Platform-Admin: true (optional)
//
// It must never be enabled in a deployment exposed to untrusted callers.
type HeaderAuthenticator struct{}

func (HeaderAuthenticator) Authenticate(r *http.Request) (rbac.Principal, error) {
	id := strings.TrimSpace(r.Header.Get("X-Baseline-Principal"))
	if id == "" {
		return rbac.Principal{}, ErrUnauthenticated
	}
	return rbac.Principal{
		ID:            id,
		PlatformAdmin: strings.EqualFold(r.Header.Get("X-Baseline-Platform-Admin"), "true"),
	}, nil
}

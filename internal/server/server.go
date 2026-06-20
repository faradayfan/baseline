package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/rbac"
)

// Server holds the wired dependencies and exposes an http.Handler.
type Server struct {
	pool *pgxpool.Pool
	ns   *namespaces.Repo
	rbac *rbac.Repo
	auth Authenticator
}

// New constructs a Server from a pool and an authenticator. The authenticator is
// injected so tests can supply HeaderAuthenticator and production can supply
// OIDC/mTLS without changing handler code.
func New(pool *pgxpool.Pool, auth Authenticator) *Server {
	return &Server{
		pool: pool,
		ns:   namespaces.NewRepo(pool),
		rbac: rbac.NewRepo(pool),
		auth: auth,
	}
}

// Handler builds the chi router. /healthz is unauthenticated; everything under
// /v1 requires authentication and carries resolved entitlements.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		if err := s.pool.Ping(req.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "unhealthy")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(Authn(s.auth, s.rbac))

		r.Route("/namespaces", func(r chi.Router) {
			r.Get("/", s.listNamespaces)
			r.Post("/", s.createNamespace)
			r.Get("/{id}", s.getNamespace)
			r.Patch("/{id}", s.patchNamespacePolicy)

			r.Get("/{id}/members", s.listMembers)
			r.Post("/{id}/members", s.addMember)
			r.Delete("/{id}/members/{principal}", s.removeMember)
		})
	})

	return r
}

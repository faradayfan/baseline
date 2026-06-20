package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/autopromote"
	"github.com/faradayfan/baseline/internal/autopromote/simple"
	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/promotions"
	"github.com/faradayfan/baseline/internal/rbac"
)

// Server holds the wired dependencies and exposes an http.Handler.
type Server struct {
	pool     *pgxpool.Pool
	ns       *namespaces.Repo
	rbac     *rbac.Repo
	promos   *promotions.Service
	factsSvc *facts.Service
	engines  *autopromote.Registry
	auth     Authenticator
}

// New constructs a Server from a pool and an authenticator. The authenticator is
// injected so tests can supply HeaderAuthenticator and production can supply
// OIDC/mTLS without changing handler code. The auto-promote registry is built
// here with the engines this build ships (currently simple/v1).
func New(pool *pgxpool.Pool, auth Authenticator) *Server {
	nsRepo := namespaces.NewRepo(pool)
	engines := autopromote.NewRegistry(simple.New())
	return &Server{
		pool:     pool,
		ns:       nsRepo,
		rbac:     rbac.NewRepo(pool),
		promos:   promotions.NewService(pool, nsRepo, engines),
		factsSvc: facts.NewService(pool),
		engines:  engines,
		auth:     auth,
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

		r.Route("/promotions", func(r chi.Router) {
			r.Post("/", s.createPromotion)
			r.Get("/", s.listPromotions)
			r.Get("/{id}", s.getPromotion)
			r.Post("/{id}/submit", s.promotionAction("submit"))
			r.Post("/{id}/approve", s.promotionAction("approve"))
			r.Post("/{id}/reject", s.promotionAction("reject"))
			r.Post("/{id}/request-changes", s.promotionAction("request-changes"))
			r.Post("/{id}/withdraw", s.promotionAction("withdraw"))
		})

		r.Route("/facts", func(r chi.Router) {
			r.Get("/{id}", s.getFact)
			r.Get("/{id}/history", s.getFactHistory)
			r.Patch("/{id}", s.revokeFact)
		})
	})

	return r
}

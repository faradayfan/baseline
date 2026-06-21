package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/autopromote"
	"github.com/faradayfan/baseline/internal/autopromote/simple"
	"github.com/faradayfan/baseline/internal/contextsvc"
	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/memory"
	"github.com/faradayfan/baseline/internal/memory/null"
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
	context  *contextsvc.Service
	engines  *autopromote.Registry
	auth     Authenticator

	// mem is the configured memory source. Reads go through contextsvc; this
	// reference exists only so the out-of-band capture handler (POST /v1/memories)
	// can type-assert for memory.Writer. nil-safe: the null source is non-nil but
	// does not implement Writer, so writes 501.
	mem memory.Source

	// middleware are applied (outermost-first) around the whole router — e.g. the
	// OTEL span middleware. Optional; set via Use.
	middleware []func(http.Handler) http.Handler

	// embedder embeds the `q` text for semantic fact search (§11.1). Optional —
	// nil in standards-only / no-Ollama deployments, where /facts?q= falls back to
	// substring. Set via SetEmbedder.
	embedder Embedder
}

// Embedder embeds query text for semantic search. embed.Client satisfies it;
// kept as an interface so the server package does not import embed directly and
// tests can inject a deterministic stub.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Use registers a middleware applied around the entire router (e.g. OTEL spans).
// Call before Handler.
func (s *Server) Use(mw func(http.Handler) http.Handler) { s.middleware = append(s.middleware, mw) }

// SetLatencyRecorder attaches the approval-latency recorder to the promotion
// workflow (the metrics.Metrics satisfies promotions.LatencyRecorder).
func (s *Server) SetLatencyRecorder(r promotions.LatencyRecorder) {
	s.promos.WithLatencyRecorder(r)
}

// SetEmbedder attaches the fact embedder, used both for semantic search (the
// read path, via this server) and for embedding facts on activation (the write
// path, via the promotion service). One embedder, both paths.
func (s *Server) SetEmbedder(e Embedder) {
	s.embedder = e
	s.promos.WithEmbedder(e)
}

// New constructs a Server with the null memory source (standards-only). Use
// NewWithMemory to supply a real backend. The authenticator is injected so tests
// use HeaderAuthenticator and production uses OIDC/mTLS without handler changes.
func New(pool *pgxpool.Pool, auth Authenticator) *Server {
	return NewWithMemory(pool, auth, null.New())
}

// NewWithMemory constructs a Server with an explicit memory.Source (selected
// from MEMORY_SOURCE at startup). The auto-promote registry is built here with
// the engines this build ships (currently simple/v1).
func NewWithMemory(pool *pgxpool.Pool, auth Authenticator, mem memory.Source) *Server {
	nsRepo := namespaces.NewRepo(pool)
	engines := autopromote.NewRegistry(simple.New())
	return &Server{
		pool:     pool,
		ns:       nsRepo,
		rbac:     rbac.NewRepo(pool),
		promos:   promotions.NewService(pool, nsRepo, engines),
		factsSvc: facts.NewService(pool),
		context:  contextsvc.NewService(pool, mem),
		engines:  engines,
		auth:     auth,
		mem:      mem,
	}
}

// Handler builds the chi router. /healthz is unauthenticated; everything under
// /v1 requires authentication and carries resolved entitlements.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	// Liveness: the process is up. Deliberately does NOT touch the DB — a
	// transient DB blip should not cause k8s to kill and restart the pod.
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Readiness: the service can serve requests, i.e. the DB is reachable. k8s
	// gates traffic on this, so a DB outage takes the pod out of rotation
	// (rather than killing it).
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := s.pool.Ping(req.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
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
			r.Get("/", s.listFacts)
			r.Get("/{id}", s.getFact)
			r.Get("/{id}/history", s.getFactHistory)
			r.Patch("/{id}", s.revokeFact)
		})

		r.Get("/context", s.getContext)

		// Out-of-band memory capture (§11 boundary note): a thin pass-through to
		// the backend's write API so the agent harness has ONE Baseline URL to post
		// raw memories to. Not part of the governance read-path; 501s when the
		// configured source can't write (e.g. standards-only / null source).
		r.Post("/memories", s.addMemory)
	})

	// Apply registered middleware outermost-first (e.g. OTEL spans wrap everything).
	var h http.Handler = r
	for i := len(s.middleware) - 1; i >= 0; i-- {
		h = s.middleware[i](h)
	}
	return h
}

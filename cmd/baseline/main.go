// Command baseline is the Facts Management Service entrypoint. It wires config →
// store → HTTP server. Domain wiring is added milestone by milestone (spec §17).
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"

	"github.com/faradayfan/baseline/internal/embed"
	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/mcpbridge"
	"github.com/faradayfan/baseline/internal/memory"
	"github.com/faradayfan/baseline/internal/memory/mem0"
	"github.com/faradayfan/baseline/internal/memory/null"
	"github.com/faradayfan/baseline/internal/metrics"
	"github.com/faradayfan/baseline/internal/platform"
	"github.com/faradayfan/baseline/internal/reaper"
	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/store"
)

func main() {
	log := platform.NewLogger()

	cfg, err := platform.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("store open", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// Reaper mode (M5): run one staleness pass and exit. Deployed as a CronJob
	// (§13). active facts past valid_to become expired, each with an audit event.
	if os.Getenv("BASELINE_REAP") == "true" {
		res, err := reaper.New(st.Pool).Reap(ctx)
		if err != nil {
			log.Error("reap", "err", err)
			os.Exit(1)
		}
		log.Info("reap complete", "expired", res.Expired, "expiring_24h", res.ExpiringSoon)
		return
	}

	emb := buildEmbedder(cfg)

	// Embedding backfill mode (§11.1): embed every active fact whose embedding is
	// NULL and exit. Run as a one-off Job after enabling semantic search, or as a
	// CronJob to self-heal facts that activated during an embedder outage.
	if os.Getenv("BASELINE_EMBED_BACKFILL") == "true" {
		if emb == nil {
			log.Error("embed backfill", "err", "EMBEDDER_URL not configured")
			os.Exit(1)
		}
		res, err := facts.BackfillEmbeddings(ctx, st.Pool, emb)
		if err != nil {
			log.Error("embed backfill", "err", err)
			os.Exit(1)
		}
		log.Info("embed backfill complete", "scanned", res.Scanned, "embedded", res.Embedded, "failed", res.Failed)
		return
	}

	// Observability (§13): meter provider + per-request spans + named metrics.
	ot := platform.SetupOTel("baseline")
	defer func() { _ = ot.Shutdown(context.Background()) }()
	mx, err := metrics.New(otel.Meter("baseline"), st.Pool)
	if err != nil {
		log.Error("metrics", "err", err)
		os.Exit(1)
	}

	// NOTE: HeaderAuthenticator is for local/dev use only. Production must wire
	// an OIDC/mTLS authenticator here (§13) before exposing the service.
	app := server.NewWithMemory(st.Pool, server.HeaderAuthenticator{}, memorySource(cfg))
	app.Use(ot.SpanMiddleware)
	app.SetLatencyRecorder(mx)
	if emb != nil {
		// Wires both paths: semantic /facts?q= search and fact embedding on
		// activation. nil → search falls back to substring, facts activate
		// without embeddings (standards-only / no-Ollama).
		app.SetEmbedder(emb)
	}

	// MCP-over-stdio mode (M4): serve the thin tool bridge instead of HTTP. The
	// principal comes from BASELINE_MCP_PRINCIPAL (dev seam; production resolves
	// it from the transport's auth before constructing the bridge).
	if os.Getenv("BASELINE_MCP_STDIO") == "true" {
		principal := os.Getenv("BASELINE_MCP_PRINCIPAL")
		bridge := mcpbridge.New(app.Handler(), principal)
		log.Info("serving MCP over stdio", "principal", principal)
		if err := bridge.Server().Run(ctx, &mcp.StdioTransport{}); err != nil {
			log.Error("mcp serve", "err", err)
			os.Exit(1)
		}
		return
	}

	// Compose the top-level handler: the REST API (incl. /healthz, /readyz) plus,
	// when enabled, the MCP-over-HTTP transport mounted at /mcp so remote clients
	// can connect over the network (per-request principal from the header).
	restHandler := app.Handler()
	var topHandler http.Handler = restHandler
	mcpHTTP := os.Getenv("BASELINE_MCP_HTTP") == "true"
	if mcpHTTP {
		mux := http.NewServeMux()
		mux.Handle("/mcp", mcpbridge.HTTPHandler(restHandler))
		mux.Handle("/mcp/", mcpbridge.HTTPHandler(restHandler))
		mux.Handle("/", restHandler)
		topHandler = mux
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           topHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.Addr, "memory_source", cfg.MemorySource, "mcp_http", mcpHTTP)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

// memorySource selects the memory backend adapter from config (§11). Config
// validation has already guaranteed mem0 has a URL and the kind is known, so the
// default arm (zep/letta not yet implemented) falls back to standards-only.
// buildEmbedder constructs the fact embedder, or nil when EMBEDDER_URL is unset
// (standards-only / no-Ollama: search degrades to substring, facts activate
// without embeddings). Returns a concrete *embed.Client, which satisfies the
// server's and promotions' Embedder interfaces.
func buildEmbedder(cfg platform.Config) *embed.Client {
	if cfg.EmbedderURL == "" {
		return nil
	}
	return embed.New(cfg.EmbedderURL, cfg.EmbedderModel, cfg.EmbedderDims)
}

func memorySource(cfg platform.Config) memory.Source {
	switch cfg.MemorySource {
	case platform.MemoryMem0:
		return mem0.New(cfg.Mem0URL, cfg.Mem0APIKey)
	case platform.MemoryNone:
		return null.New()
	default:
		// zep/letta adapters land later; until then, run standards-only.
		return null.New()
	}
}

var _ memory.Source = null.Source{}

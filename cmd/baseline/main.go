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

	"github.com/faradayfan/baseline/internal/platform"
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

	// NOTE: HeaderAuthenticator is for local/dev use only. Production must wire
	// an OIDC/mTLS authenticator here (§13) before exposing the service.
	app := server.New(st.Pool, server.HeaderAuthenticator{})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.Addr, "memory_source", cfg.MemorySource)
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

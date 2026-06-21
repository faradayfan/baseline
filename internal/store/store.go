// Package store owns the Postgres connection pool and migration runner. Domain
// packages receive a *Store (or the embedded *pgxpool.Pool) and never open their
// own connections.
package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store wraps the pgx pool. It is safe for concurrent use.
type Store struct {
	Pool *pgxpool.Pool
}

// Open creates a connection pool against databaseURL and verifies connectivity.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: new pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close releases all pooled connections.
func (s *Store) Close() { s.Pool.Close() }

// Migrate runs all pending goose migrations embedded in this package. It opens a
// short-lived database/sql connection (goose's interface) over the same DSN.
func Migrate(ctx context.Context, databaseURL string) error {
	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("store: set dialect: %w", err)
	}

	db, err := goose.OpenDBWithDriver("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("store: open for migrate: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("store: migrate up: %w", err)
	}
	return nil
}

// ensure the pgx stdlib driver is registered for goose's database/sql usage.
var _ = stdlib.GetDefaultDriver

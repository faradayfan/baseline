// Package storetest provides a shared Postgres+pgvector test harness backed by
// testcontainers. It is imported only by _test.go files.
//
// Usage: one container per test package. Call New(t) once (typically guarded so
// it's reused across the package via sync.Once in a TestMain or a package-level
// helper), then use the returned Harness to get isolated database handles:
//
//   - h.Tx(t)      — a transaction rolled back at test end. Fast, fully isolated,
//     the default. Cannot observe COMMITs or cross-tx behavior.
//   - h.FreshDB(t) — a brand-new database with migrations applied, dropped at
//     test end. Use for committed-state and concurrency tests
//     (e.g. the facts_active_unique index §14.2, optimistic
//     concurrency 409 §14.8) that tx-rollback cannot exercise.
package storetest

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/faradayfan/baseline/internal/store"
)

// Harness owns a running pgvector container and the admin connection used to
// mint per-test databases. Construct one per test package with New.
type Harness struct {
	container *postgres.PostgresContainer
	baseDSN   string // DSN to the template database, migrations already applied
	adminPool *pgxpool.Pool
	dbCounter atomic.Int64
}

// New starts a pgvector container and applies migrations to the default database
// (which becomes the template every FreshDB clones). It is slow (pulls/boots an
// image) — call once per package, not per test.
//
// New does NOT register cleanup: the caller owns the container's lifetime via
// Close. Use Shared + TestMain for the common per-package singleton pattern,
// which handles teardown correctly. (Registering teardown on the first test's
// *testing.T would terminate the container as soon as that one test finished,
// breaking every later test in the package.)
func New() (*Harness, error) {
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("baseline"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storetest: start container: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("storetest: connection string: %w", err)
	}

	// Apply migrations to the default DB; FreshDB uses it as a CREATE DATABASE template.
	if err := store.Migrate(ctx, dsn); err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("storetest: migrate template: %w", err)
	}

	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, fmt.Errorf("storetest: admin pool: %w", err)
	}

	return &Harness{container: container, baseDSN: dsn, adminPool: adminPool}, nil
}

// Close releases the admin pool and terminates the container. Call once per
// package, typically from TestMain after m.Run().
func (h *Harness) Close() {
	if h == nil {
		return
	}
	h.adminPool.Close()
	_ = h.container.Terminate(context.Background())
}

// Pool returns a pool to the template database. Prefer Tx or FreshDB for
// isolation; use this only for read-only inspection (e.g. asserting indexes).
func (h *Harness) Pool() *pgxpool.Pool { return h.adminPool }

// Tx opens a transaction against the template database and rolls it back when
// the test ends. Fully isolated and fast — the default for repo tests that don't
// need to observe COMMITs.
func (h *Harness) Tx(t *testing.T) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	conn, err := h.adminPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("storetest: acquire conn: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()
		t.Fatalf("storetest: begin tx: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.Background())
		conn.Release()
	})
	return tx
}

// FreshDB creates a uniquely-named database cloned from the migrated template,
// returns a pool to it, and drops it at test end. Use for committed-state and
// concurrency tests that tx-rollback cannot exercise.
func (h *Harness) FreshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), h.dbCounter.Add(1))

	// CREATE DATABASE ... TEMPLATE clones the migrated schema instantly.
	if _, err := h.adminPool.Exec(ctx,
		fmt.Sprintf(`CREATE DATABASE %s TEMPLATE baseline`, name)); err != nil {
		t.Fatalf("storetest: create db %s: %v", name, err)
	}

	dsn := swapDBName(h.baseDSN, name)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("storetest: pool for %s: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Terminate any lingering backends before DROP.
		_, _ = h.adminPool.Exec(context.Background(),
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, name)
		_, _ = h.adminPool.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, name))
	})
	return pool
}

// swapDBName replaces the path component (database name) of a postgres DSN.
func swapDBName(dsn, newName string) string {
	// DSN form: postgres://user:pass@host:port/dbname?query
	at := strings.LastIndex(dsn, "@")
	rest := dsn[at:]
	slash := strings.Index(rest, "/")
	q := strings.Index(rest, "?")
	if q == -1 {
		return dsn[:at] + rest[:slash+1] + newName
	}
	return dsn[:at] + rest[:slash+1] + newName + rest[q:]
}

// shared is the per-package singleton populated by Main and read by Shared.
var shared *Harness

// Main runs a test package's main with a single shared Harness booted before
// the tests and torn down after. Use it from TestMain so every test in the
// package reuses one container:
//
//	func TestMain(m *testing.M) { storetest.Main(m) }
//
// Under `go test -short` no container is booted (integration tests self-skip on
// testing.Short()), so unit tests in the package still run fast and Docker-free.
// Otherwise, if Docker is unavailable Main fails fast with a clear message
// rather than letting each test error obscurely.
func Main(m *testing.M) {
	// flags must be parsed before testing.Short() is readable.
	flag.Parse()

	if testing.Short() {
		os.Exit(m.Run())
	}

	h, err := New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "storetest: cannot start harness (is Docker running?): %v\n", err)
		os.Exit(1)
	}
	shared = h
	code := m.Run()
	h.Close()
	os.Exit(code)
}

// Shared returns the per-package Harness set up by Main. It panics if Main was
// not used (i.e. the package has no TestMain calling storetest.Main).
func Shared(t *testing.T) *Harness {
	t.Helper()
	if shared == nil {
		t.Fatal("storetest.Shared: no harness — add `func TestMain(m *testing.M){ storetest.Main(m) }` to this package")
	}
	return shared
}

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/faradayfan/baseline/internal/storetest"
)

// TestMigrations_Applied verifies the schema and the invariant-enforcing indexes
// exist after migration (spec §12, §14.2). The template DB is migrated by the
// harness, so we inspect it directly.
func TestMigrations_Applied(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := storetest.Shared(t)
	pool := h.Pool()
	ctx := context.Background()

	wantTables := []string{"namespaces", "facts", "promotion_requests", "memberships", "audit_events"}
	for _, tbl := range wantTables {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s missing", tbl)
		}
	}

	// The partial unique index that enforces "one active fact per
	// (namespace, canonical_key)" (§14.2) must exist and be partial on status.
	var indexdef string
	err := pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'facts_active_unique'`).Scan(&indexdef)
	if err != nil {
		t.Fatalf("facts_active_unique index not found: %v", err)
	}
	if !strings.Contains(indexdef, "UNIQUE") || !strings.Contains(indexdef, "status = 'active'") {
		t.Errorf("facts_active_unique not a partial unique index: %s", indexdef)
	}

	// The hnsw vector index for fact search must exist.
	var hnsw string
	err = pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'facts_embedding_idx'`).Scan(&hnsw)
	if err != nil {
		t.Fatalf("facts_embedding_idx not found: %v", err)
	}
	if !strings.Contains(hnsw, "hnsw") {
		t.Errorf("facts_embedding_idx is not hnsw: %s", hnsw)
	}
}

// TestActiveUniqueInvariant proves the DB rejects two active facts sharing
// (namespace_id, canonical_key) — the core invariant, §14.2. This needs
// committed inserts, so it uses a fresh database rather than a rolled-back tx.
func TestActiveUniqueInvariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := storetest.Shared(t)
	db := h.FreshDB(t)
	ctx := context.Background()

	var nsID string
	if err := db.QueryRow(ctx,
		`INSERT INTO namespaces (name, kind) VALUES ('org', 'org') RETURNING id`).Scan(&nsID); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	insertActive := func() error {
		_, err := db.Exec(ctx,
			`INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by)
			 VALUES ($1, 'x', '{}'::jsonb, 'build.command:foo', 'active', 'tester')`, nsID)
		return err
	}

	if err := insertActive(); err != nil {
		t.Fatalf("first active insert should succeed: %v", err)
	}
	if err := insertActive(); err == nil {
		t.Fatal("second active fact with same canonical_key must violate facts_active_unique, but insert succeeded")
	}

	// A non-active fact with the same key is allowed (only active is constrained).
	_, err := db.Exec(ctx,
		`INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by)
		 VALUES ($1, 'x', '{}'::jsonb, 'build.command:foo', 'superseded', 'tester')`, nsID)
	if err != nil {
		t.Errorf("superseded fact with same key should be allowed: %v", err)
	}
}

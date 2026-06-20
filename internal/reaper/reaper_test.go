package reaper_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/reaper"
	"github.com/faradayfan/baseline/internal/storetest"
)

func seedFact(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, key, status string, validTo *time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, valid_to, created_by, valid_from)
		VALUES ($1,'s','{}'::jsonb,$2,$3,'{}',$4,'seed',now()) RETURNING id`,
		ns, key, status, validTo).Scan(&id)
	if err != nil {
		t.Fatalf("seed fact: %v", err)
	}
	return id
}

func setupNS(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind) VALUES ('org','org') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func factStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM facts WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReap_ExpiresOnlyPastValidTo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := setupNS(t, pool)
	ctx := context.Background()

	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(48 * time.Hour)

	stale := seedFact(t, pool, ns, "stale", "active", &past)
	live := seedFact(t, pool, ns, "live", "active", &future)
	noExpiry := seedFact(t, pool, ns, "forever", "active", nil)

	res, err := reaper.New(pool).Reap(ctx)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if res.Expired != 1 {
		t.Errorf("Expired = %d, want 1", res.Expired)
	}
	if factStatus(t, pool, stale) != "expired" {
		t.Error("past-valid_to fact should be expired")
	}
	if factStatus(t, pool, live) != "active" {
		t.Error("future-valid_to fact must stay active")
	}
	if factStatus(t, pool, noExpiry) != "active" {
		t.Error("no-expiry fact must stay active")
	}
}

func TestReap_WritesAuditEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := setupNS(t, pool)
	ctx := context.Background()
	past := time.Now().Add(-1 * time.Hour)
	id := seedFact(t, pool, ns, "stale", "active", &past)

	if _, err := reaper.New(pool).Reap(ctx); err != nil {
		t.Fatal(err)
	}
	var principal, action, from, to string
	err := pool.QueryRow(ctx, `
		SELECT principal, action, from_state, to_state FROM audit_events
		WHERE subject_id = $1 AND action = 'fact.expired'`, id).Scan(&principal, &action, &from, &to)
	if err != nil {
		t.Fatalf("expected an audit event: %v", err)
	}
	if principal != "system:reaper" || from != "active" || to != "expired" {
		t.Errorf("audit = {%s %s->%s}, want system:reaper active->expired", principal, from, to)
	}
}

func TestReap_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := setupNS(t, pool)
	ctx := context.Background()
	past := time.Now().Add(-1 * time.Hour)
	seedFact(t, pool, ns, "stale", "active", &past)

	first, _ := reaper.New(pool).Reap(ctx)
	second, _ := reaper.New(pool).Reap(ctx)
	if first.Expired != 1 || second.Expired != 0 {
		t.Errorf("idempotency: first=%d second=%d, want 1 then 0", first.Expired, second.Expired)
	}
}

func TestReap_CountsExpiringSoon(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := setupNS(t, pool)
	ctx := context.Background()

	soon := time.Now().Add(6 * time.Hour)   // within 24h
	later := time.Now().Add(72 * time.Hour) // beyond 24h
	seedFact(t, pool, ns, "soon", "active", &soon)
	seedFact(t, pool, ns, "later", "active", &later)

	res, err := reaper.New(pool).Reap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExpiringSoon != 1 {
		t.Errorf("ExpiringSoon = %d, want 1", res.ExpiringSoon)
	}
}

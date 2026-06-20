package contextsvc_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/contextsvc"
	"github.com/faradayfan/baseline/internal/memory"
	"github.com/faradayfan/baseline/internal/memory/null"
	"github.com/faradayfan/baseline/internal/storetest"
)

// fakeSource is an in-memory MemorySource for merge tests.
type fakeSource struct{ mems []memory.Memory }

func (f fakeSource) List(context.Context, string, memory.ListOpts) ([]memory.Memory, error) {
	return f.mems, nil
}
func (f fakeSource) Search(context.Context, string, string, memory.SearchOpts) ([]memory.Memory, error) {
	return f.mems, nil
}
func (f fakeSource) Get(context.Context, string) (memory.Memory, error) {
	return memory.Memory{}, memory.ErrNotFound
}

func seedOrgFact(t *testing.T, pool *pgxpool.Pool, key, statement string) uuid.UUID {
	t.Helper()
	var ns uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind) VALUES ('org','org') RETURNING id`).Scan(&ns); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by, valid_from)
		VALUES ($1,$2,'{}'::jsonb,$3,'active','seed',now())`, ns, statement, key); err != nil {
		t.Fatal(err)
	}
	return ns
}

// seedTaggedFact inserts an active fact with tags into an existing namespace.
func seedTaggedFact(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, key, statement string, tags []string) {
	t.Helper()
	if tags == nil {
		tags = []string{}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
		VALUES ($1,$2,'{}'::jsonb,$3,'active',$4,'seed',now())`, ns, statement, key, tags); err != nil {
		t.Fatal(err)
	}
}

// TestResolve_TagFilter covers the three tag-filter behaviors: empty tags =
// passthrough (all facts), supplied tags = ANY-match narrowing, and the
// authoritative:true bypass (always returned regardless of tags).
func TestResolve_TagFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ctx := context.Background()
	var ns uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO namespaces (name, kind) VALUES ('org','org') RETURNING id`).Scan(&ns); err != nil {
		t.Fatal(err)
	}
	seedTaggedFact(t, pool, ns, "sec:tls", "use mTLS", []string{"security", "backend"})
	seedTaggedFact(t, pool, ns, "fe:bundle", "tree-shake the bundle", []string{"frontend"})
	seedTaggedFact(t, pool, ns, "base:ci", "deploys go through CI", []string{"authoritative:true"})

	svc := contextsvc.NewService(pool, null.New())
	keys := func(tags []string) map[string]bool {
		items, err := svc.Resolve(ctx, contextsvc.Query{Namespaces: []uuid.UUID{ns}, Tags: tags})
		if err != nil {
			t.Fatalf("resolve %v: %v", tags, err)
		}
		out := map[string]bool{}
		for _, it := range items {
			out[it.CanonicalKey] = true
		}
		return out
	}

	// Empty tags → everything (passthrough, backward-compatible).
	all := keys(nil)
	if !all["sec:tls"] || !all["fe:bundle"] || !all["base:ci"] || len(all) != 3 {
		t.Errorf("no tags should return all 3 facts, got %v", all)
	}

	// tags=[security] → the security fact (ANY-match) + the authoritative one (bypass),
	// NOT the frontend one.
	sec := keys([]string{"security"})
	if !sec["sec:tls"] || !sec["base:ci"] {
		t.Errorf("tags=security should include the security + authoritative facts, got %v", sec)
	}
	if sec["fe:bundle"] {
		t.Errorf("tags=security must NOT include the frontend fact, got %v", sec)
	}

	// tags=[devops] (matches nothing) → only the authoritative baseline survives.
	none := keys([]string{"devops"})
	if len(none) != 1 || !none["base:ci"] {
		t.Errorf("non-matching tags should return only the authoritative fact, got %v", none)
	}

	// OR-match: tags=[frontend, security] → both tagged facts + authoritative.
	both := keys([]string{"frontend", "security"})
	if !both["sec:tls"] || !both["fe:bundle"] || !both["base:ci"] {
		t.Errorf("OR-match should include both tagged facts + authoritative, got %v", both)
	}
}

// TestResolve_TierAlwaysBypass asserts the `tier:always` delivery tier: a fact
// tagged tier:always ALWAYS passes the read-path tag filter (so a SessionStart
// hook can fetch exactly the always-on set via ?tags=tier:always), AND that this
// is purely a delivery concern — tier:always does NOT affect precedence (it is
// orthogonal to authoritative:true, which does). This is the load-bearing
// separation between "when injected" (tier) and "what wins" (authoritative).
func TestResolve_TierAlwaysBypass(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ctx := context.Background()
	var org, team uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO namespaces (name, kind) VALUES ('org','org') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO namespaces (name, kind, parent_id) VALUES ('team:eng','team',$1) RETURNING id`, org).Scan(&team); err != nil {
		t.Fatal(err)
	}

	// A guardrail tagged tier:always (topic 'testing'); an unrelated semantic fact.
	seedTaggedFact(t, pool, org, "guard:tests", "run tests before commit", []string{"tier:always", "testing"})
	seedTaggedFact(t, pool, org, "sem:auth", "auth uses JWT", []string{"backend", "auth"})

	svc := contextsvc.NewService(pool, null.New())
	keys := func(tags []string) map[string]bool {
		items, err := svc.Resolve(ctx, contextsvc.Query{Namespaces: []uuid.UUID{org, team}, Tags: tags})
		if err != nil {
			t.Fatalf("resolve %v: %v", tags, err)
		}
		out := map[string]bool{}
		for _, it := range items {
			out[it.CanonicalKey] = true
		}
		return out
	}

	// A SessionStart-style query: tags=[tier:always] returns ONLY the always-on
	// fact (matches as a literal tag) — the unrelated semantic fact is filtered out.
	always := keys([]string{"tier:always"})
	if !always["guard:tests"] {
		t.Errorf("tags=tier:always must include the guardrail, got %v", always)
	}
	if always["sem:auth"] {
		t.Errorf("tags=tier:always must NOT include the unrelated semantic fact, got %v", always)
	}

	// The bypass: a tag filter that matches NOTHING still returns the tier:always
	// fact (it always passes the read-path filter, like authoritative does).
	bypass := keys([]string{"nonexistent-topic"})
	if !bypass["guard:tests"] {
		t.Errorf("tier:always must bypass a non-matching tag filter, got %v", bypass)
	}
	if bypass["sem:auth"] {
		t.Errorf("non-matching filter must not surface the semantic fact, got %v", bypass)
	}

	// Orthogonality: tier:always is delivery-only and must NOT win precedence.
	// Same canonical_key in org (tier:always) and the more-specific team (plain).
	// The more-specific team fact must win — tier:always does not override.
	seedTaggedFact(t, pool, org, "build.cmd:svc", "org: make", []string{"tier:always"})
	seedTaggedFact(t, pool, team, "build.cmd:svc", "team: bazel", nil)
	var winner string
	items, err := svc.Resolve(ctx, contextsvc.Query{Namespaces: []uuid.UUID{org, team}})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.CanonicalKey == "build.cmd:svc" {
			winner = it.Statement
		}
	}
	if winner != "team: bazel" {
		t.Errorf("tier:always must NOT win precedence — expected the more-specific team fact, got %q", winner)
	}
}

// TestResolve_StandardsOnly asserts §11.2: with the null source, /context serves
// only facts and include_memories yields no memories and no error (the coupling
// guarantee — the system runs fully with the memory dependency removed).
func TestResolve_StandardsOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := seedOrgFact(t, pool, "policy:ci", "deploys via CI")

	svc := contextsvc.NewService(pool, null.New())
	items, err := svc.Resolve(context.Background(), contextsvc.Query{
		ActorID: "alice", Namespaces: []uuid.UUID{ns}, IncludeMemories: true,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(items) != 1 || items[0].Source != contextsvc.SourceFact {
		t.Errorf("expected exactly one fact and no memories, got %+v", items)
	}
}

// TestResolve_MemoryMerge asserts §10 step 5: memories append below facts and are
// de-duplicated against facts by canonical_key; memories never override a fact.
func TestResolve_MemoryMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := seedOrgFact(t, pool, "policy:ci", "fact: deploys via CI")

	src := fakeSource{mems: []memory.Memory{
		{ID: "m1", Content: "personal note", Metadata: map[string]any{}},
		{ID: "m2", Content: "dup of fact", Metadata: map[string]any{"canonical_key": "policy:ci"}},
	}}
	svc := contextsvc.NewService(pool, src)
	items, err := svc.Resolve(context.Background(), contextsvc.Query{
		ActorID: "alice", Namespaces: []uuid.UUID{ns}, IncludeMemories: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Expect: the fact first, then the non-duplicate memory; the dup is dropped.
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (fact + 1 memory)", len(items))
	}
	if items[0].Source != contextsvc.SourceFact {
		t.Error("facts must come before memories (precedence)")
	}
	if items[1].Source != contextsvc.SourceMemory || items[1].Statement != "personal note" {
		t.Errorf("second item should be the non-dup memory, got %+v", items[1])
	}
}

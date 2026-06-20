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

package facts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/storetest"
)

func TestMain(m *testing.M) { storetest.Main(m) }

// vec768 builds a 768-dim unit-ish vector that is "close" to a one-hot axis: a 1.0
// in slot `axis`, small noise elsewhere. Cosine distance between two such vectors
// is small iff they share an axis — giving deterministic, assertable ranking
// without a live embedder.
func vec768(axis int) []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 0.001
	}
	v[axis] = 1.0
	return v
}

// seedActiveFact inserts a fact straight to active in ns, optionally with an
// embedding. Bypasses the promotion workflow — this test exercises the repo read
// path, not governance.
func seedActiveFact(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, statement string, embedAxis int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	f, err := facts.Insert(ctx, pool, facts.Fact{
		NamespaceID: ns,
		Statement:   statement,
		Subject:     facts.Subject{Type: "t." + statement},
		Status:      facts.StatusActive,
		CreatedBy:   "seed",
	})
	if err != nil {
		t.Fatalf("insert %q: %v", statement, err)
	}
	if embedAxis >= 0 {
		if err := facts.SetEmbedding(ctx, pool, f.ID, vec768(embedAxis)); err != nil {
			t.Fatalf("set embedding %q: %v", statement, err)
		}
	}
	return f.ID
}

func mkNS(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	ns, err := namespaces.NewRepo(pool).Create(context.Background(),
		namespaces.Namespace{Name: name, Kind: namespaces.KindOrg})
	if err != nil {
		t.Fatalf("seed ns %s: %v", name, err)
	}
	return ns.ID
}

// TestList_SemanticRanking asserts QueryVec orders results by cosine distance:
// the fact whose embedding shares the query's axis ranks first.
func TestList_SemanticRanking(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := storetest.Shared(t).FreshDB(t)
	ns := mkNS(t, pool, "org")

	near := seedActiveFact(t, pool, ns, "deploys go through CI", 5)
	far := seedActiveFact(t, pool, ns, "the sky is blue", 200)

	got, err := facts.List(ctx, pool, facts.ListFilter{
		Namespaces: []uuid.UUID{ns},
		QueryVec:   vec768(5), // query shares axis 5 with `near`
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 facts, got %d", len(got))
	}
	if got[0].ID != near {
		t.Errorf("nearest fact should rank first: got %s, want %s (near)", got[0].ID, near)
	}
	if got[1].ID != far {
		t.Errorf("far fact should rank last: got %s, want %s (far)", got[1].ID, far)
	}
}

// TestList_NullEmbeddingRanksLast asserts a fact with no embedding is still
// returned (findable), just after the embedded ones (ORDER BY ... NULLS LAST).
func TestList_NullEmbeddingRanksLast(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := storetest.Shared(t).FreshDB(t)
	ns := mkNS(t, pool, "org")

	embedded := seedActiveFact(t, pool, ns, "has an embedding", 5)
	noEmbed := seedActiveFact(t, pool, ns, "never embedded", -1) // NULL embedding

	got, err := facts.List(ctx, pool, facts.ListFilter{
		Namespaces: []uuid.UUID{ns},
		QueryVec:   vec768(5),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("NULL-embedding fact must NOT be dropped: want 2, got %d", len(got))
	}
	if got[0].ID != embedded {
		t.Errorf("embedded fact should rank first, got %s", got[0].ID)
	}
	if got[1].ID != noEmbed {
		t.Errorf("NULL-embedding fact should rank last, got %s", got[1].ID)
	}
}

// TestList_SemanticSearchHonorsNamespaceScope is the leak test: semantic ranking
// must NEVER surface a fact outside the caller's entitled namespaces, even when
// that out-of-scope fact is the closest vector match.
func TestList_SemanticSearchHonorsNamespaceScope(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := storetest.Shared(t).FreshDB(t)
	mine := mkNS(t, pool, "mine")
	theirs := mkNS(t, pool, "theirs")

	// The BEST vector match lives in a namespace the caller cannot read.
	secret := seedActiveFact(t, pool, theirs, "secret, perfect match", 5)
	visible := seedActiveFact(t, pool, mine, "mine, weaker match", 200)

	got, err := facts.List(ctx, pool, facts.ListFilter{
		Namespaces: []uuid.UUID{mine}, // only the caller's namespace
		QueryVec:   vec768(5),         // closest to `secret`
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, f := range got {
		if f.ID == secret {
			t.Fatal("LEAK: semantic search returned a fact outside the caller's namespaces")
		}
	}
	if len(got) != 1 || got[0].ID != visible {
		t.Fatalf("want only the in-scope fact, got %d facts", len(got))
	}
}

// TestBackfillEmbeddings asserts the backfill embeds NULL active facts and is
// idempotent, using a deterministic stub embedder.
func TestBackfillEmbeddings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := storetest.Shared(t).FreshDB(t)
	ns := mkNS(t, pool, "org")

	seedActiveFact(t, pool, ns, "fact one", -1)
	seedActiveFact(t, pool, ns, "fact two", -1)
	seedActiveFact(t, pool, ns, "already embedded", 5)

	res, err := facts.BackfillEmbeddings(ctx, pool, stubEmbedder{})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if res.Scanned != 2 || res.Embedded != 2 || res.Failed != 0 {
		t.Fatalf("want scanned=2 embedded=2 failed=0, got %+v", res)
	}

	// Idempotent: a second pass finds nothing to do.
	res2, err := facts.BackfillEmbeddings(ctx, pool, stubEmbedder{})
	if err != nil {
		t.Fatalf("backfill 2: %v", err)
	}
	if res2.Scanned != 0 {
		t.Fatalf("second pass should find 0 NULL facts, got scanned=%d", res2.Scanned)
	}
}

// stubEmbedder returns a deterministic 768-dim vector; no network.
type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return vec768(0), nil
}

package promotions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/promotions"
)

type okEmbedder struct{}

func (okEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, 768)
	v[0] = 1.0
	return v, nil
}

type failEmbedder struct{}

func (failEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("embedder down")
}

// setupWithEmbedder builds a single-approval namespace + service with the given
// embedder. team kind requires 1 approval, keeping the flow short.
func setupWithEmbedder(t *testing.T, e promotions.Embedder) (*promotions.Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	svc, pool, _ := setup(t) // reuses service_test.go's harness (org, 2 approvals)
	// project: 1 required approval, NO auto-promote — so the deterministic
	// Submit→Approve flow runs through activate() (not maybeAutoPromote).
	ns, err := namespaces.NewRepo(pool).Create(context.Background(),
		namespaces.Namespace{Name: "project", Kind: namespaces.KindProject})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return svc.WithEmbedder(e), pool, ns.ID
}

func embeddingCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(embedding) FROM facts WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count embedding: %v", err)
	}
	return n
}

func activationAudits(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE subject_id = $1 AND action = 'fact.activated'`,
		id).Scan(&n); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	return n
}

// approveToActive drives a proposal to active in a 1-approval (team) namespace.
func approveToActive(t *testing.T, svc *promotions.Service, ns uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	p := propose(t, svc, ns, "alice", "deploys go through CI", facts.Subject{Type: "deploy.policy"})
	if _, err := svc.Submit(ctx, p.ID, p.FactID, "alice"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := svc.Approve(ctx, p.ID, "bob", "lgtm"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return p.FactID
}

// TestActivation_EmbedsFact: with an embedder wired, an activated fact gets a
// non-NULL embedding.
func TestActivation_EmbedsFact(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, team := setupWithEmbedder(t, okEmbedder{})
	id := approveToActive(t, svc, team)

	if embeddingCount(t, pool, id) != 1 {
		t.Error("activated fact should have a non-NULL embedding")
	}
	if got := activationAudits(t, pool, id); got != 1 {
		t.Errorf("want exactly 1 fact.activated audit, got %d", got)
	}
}

// TestActivation_DegradesOnEmbedderFailure: a failing embedder must NOT block
// activation — the fact goes active with a NULL embedding and exactly one audit.
func TestActivation_DegradesOnEmbedderFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, team := setupWithEmbedder(t, failEmbedder{})
	id := approveToActive(t, svc, team)

	if embeddingCount(t, pool, id) != 0 {
		t.Error("embedder failure should leave embedding NULL")
	}
	if got := activationAudits(t, pool, id); got != 1 {
		t.Errorf("embedder failure must not add/remove audits: want 1 fact.activated, got %d", got)
	}
}

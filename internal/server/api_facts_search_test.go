package server_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/storetest"
)

// stubEmbedder returns a deterministic vector keyed to the query text, so search
// ranking is assertable in CI without a live Ollama. "match" → axis 5; anything
// else → axis 200. failOn != "" makes that exact query error (to test fallback).
type stubEmbedder struct{ failOn string }

func (s stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if s.failOn != "" && text == s.failOn {
		return nil, errors.New("embedder down")
	}
	axis := 200
	if text == "match" {
		axis = 5
	}
	return axisVec(axis), nil
}

func axisVec(axis int) []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 0.001
	}
	v[axis] = 1.0
	return v
}

// newSearchAPI builds a server with a stub embedder wired (so /facts?q= ranks).
func newSearchAPI(t *testing.T, emb server.Embedder) (*storetest.API, *pgxpool.Pool) {
	t.Helper()
	h := storetest.Shared(t)
	var pool *pgxpool.Pool
	api := storetest.NewAPI(t, h, func(p *pgxpool.Pool) http.Handler {
		pool = p
		s := server.New(p, server.HeaderAuthenticator{})
		if emb != nil {
			s.SetEmbedder(emb)
		}
		return s.Handler()
	})
	return api, pool
}

// seedActiveFactEmbedded inserts an active fact and gives it an embedding on the
// given axis (axis<0 → leave NULL).
func seedActiveFactEmbedded(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, key, statement string, axis int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by)
		 VALUES ($1,$2,$3,$4,'active','seed') RETURNING id`,
		ns, statement, []byte(`{"type":"t"}`), key).Scan(&id)
	if err != nil {
		t.Fatalf("seed fact: %v", err)
	}
	if axis >= 0 {
		if err := facts.SetEmbedding(ctx, pool, id, axisVec(axis)); err != nil {
			t.Fatalf("set embedding: %v", err)
		}
	}
	return id
}

// TestSearchFacts_SemanticRanking: GET /v1/facts?q=match ranks the axis-5 fact
// first through the full HTTP path.
func TestSearchFacts_SemanticRanking(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newSearchAPI(t, stubEmbedder{})
	org := seedNamespace(t, pool, "org", "org", nil)
	grant(t, pool, "alice", org, "reader")

	near := seedActiveFactEmbedded(t, pool, org, "k:near", "near", 5)
	_ = seedActiveFactEmbedded(t, pool, org, "k:far", "far", 200)

	resp := api.Do(t, "GET", "/v1/facts?q=match", nil, hdr("alice"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got []facts.Fact
	storetest.DecodeJSON(t, resp, &got)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].ID != near {
		t.Errorf("nearest fact should rank first via HTTP, got %s", got[0].ID)
	}
}

// TestSearchFacts_NoLeakAcrossNamespaces: the leak test at the API layer — the
// best vector match in an unreadable namespace must not appear.
func TestSearchFacts_NoLeakAcrossNamespaces(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newSearchAPI(t, stubEmbedder{})
	mine := seedNamespace(t, pool, "mine", "org", nil)
	theirs := seedNamespace(t, pool, "theirs", "org", nil)
	grant(t, pool, "alice", mine, "reader") // alice can read ONLY mine

	secret := seedActiveFactEmbedded(t, pool, theirs, "k:secret", "secret", 5) // best match, unreadable
	_ = seedActiveFactEmbedded(t, pool, mine, "k:visible", "visible", 200)     // weaker, readable
	_ = theirs

	resp := api.Do(t, "GET", "/v1/facts?q=match", nil, hdr("alice"))
	var got []facts.Fact
	storetest.DecodeJSON(t, resp, &got)
	for _, f := range got {
		if f.ID == secret {
			t.Fatal("LEAK: search returned a fact outside the caller's namespaces")
		}
	}
	if len(got) != 1 {
		t.Fatalf("want only the in-scope fact, got %d", len(got))
	}
}

// TestSearchFacts_FallbackOnEmbedderError: when the embedder errors, search falls
// back to substring and still returns (never 500s).
func TestSearchFacts_FallbackOnEmbedderError(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newSearchAPI(t, stubEmbedder{failOn: "needle"})
	org := seedNamespace(t, pool, "org", "org", nil)
	grant(t, pool, "alice", org, "reader")

	// Substring "needle" matches this statement; the embedder fails on "needle"
	// so the handler must fall back to ILIKE, not error.
	seedActiveFactEmbedded(t, pool, org, "k:1", "contains a needle here", -1)
	seedActiveFactEmbedded(t, pool, org, "k:2", "unrelated haystack", -1)

	resp := api.Do(t, "GET", "/v1/facts?q=needle", nil, hdr("alice"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fallback should still return 200, got %d", resp.StatusCode)
	}
	var got []facts.Fact
	storetest.DecodeJSON(t, resp, &got)
	if len(got) != 1 || got[0].Statement != "contains a needle here" {
		t.Fatalf("substring fallback should match exactly the needle fact, got %d", len(got))
	}
}

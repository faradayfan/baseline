package conformance

import (
	"context"
	"testing"
)

// §14.16 — canonical_key is a deterministic function of subject: differently
// worded statements with the same subject collapse to one key (driving
// supersession, not duplication). Observed end-to-end: two proposals with the
// same subject but different statements yield the same canonical_key, and the
// second conflicts with the first.
func Test14_16_CanonicalKeyDeterministic(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("alice", org, "contributor")
	e.grant("bob", org, "reviewer")

	subject := map[string]any{"type": "build.command", "scope": "svc"}
	p1, f1 := e.propose(org, "alice", "use make to build", subject)
	e.do("POST", "/v1/promotions/"+p1+"/submit", nil, asPrincipal("alice")).Body.Close()
	e.do("POST", "/v1/promotions/"+p1+"/approve", map[string]any{}, asPrincipal("bob")).Body.Close()

	// Different phrasing, same subject → same key → conflict_with the first fact.
	p2, f2 := e.propose(org, "alice", "the build uses bazel now", subject)

	k1 := e.canonicalKey(f1)
	k2 := e.canonicalKey(f2)
	if k1 != k2 {
		t.Errorf("same subject yielded different keys: %q vs %q", k1, k2)
	}

	var conflictWith *string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT conflict_with::text FROM promotion_requests WHERE id = $1`, p2).Scan(&conflictWith); err != nil {
		t.Fatal(err)
	}
	if conflictWith == nil || *conflictWith != f1 {
		t.Errorf("second proposal should conflict with the first fact (supersession, not duplication)")
	}
}

// §14.17 — canonical_key is never accepted from a client: it is always recomputed
// from subject on write. A request that tries to set it directly is ignored.
func Test14_17_CanonicalKeyNotClientSet(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("alice", org, "contributor")

	// Attempt to inject a bogus canonical_key alongside a real subject.
	_, fact := e.proposeWithCandidate(org, "alice", map[string]any{
		"proposed_statement": "x",
		"subject":            map[string]any{"type": "deploy.policy", "scope": "api"},
		"canonical_key":      "totally:made-up",
	})

	got := e.canonicalKey(fact)
	want := "deploy.policy:api" // derived from subject, NOT the injected value
	if got != want {
		t.Errorf("canonical_key = %q, want %q (must be derived from subject, not client-set)", got, want)
	}
}

func (e *env) canonicalKey(factID string) string {
	e.t.Helper()
	var k string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT canonical_key FROM facts WHERE id = $1`, factID).Scan(&k); err != nil {
		e.t.Fatalf("canonicalKey: %v", err)
	}
	return k
}

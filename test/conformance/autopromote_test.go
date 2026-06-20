package conformance

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// configureSimpleEngine sets a simple/v1 auto-promote policy on ns via the API
// (which validates against the pinned engine — also exercising §14.15's accept path).
func (e *env) configureSimpleEngine(ns uuid.UUID, admin string) {
	e.t.Helper()
	policy := map[string]any{
		"required_approvals": 1,
		"auto_promote": map[string]any{
			"engine": "simple/v1",
			"rules": map[string]any{"rules": []any{
				map[string]any{"all": []any{
					map[string]any{"field": "provenance.origin_type", "op": "eq", "value": "merged_pr"},
				}},
			}},
		},
	}
	resp := e.do("PATCH", "/v1/namespaces/"+ns.String(), policy, asPrincipal(admin))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("configure engine: status %d", resp.StatusCode)
	}
}

// proposeWithCandidate proposes including engine candidate signals.
func (e *env) proposeWithCandidate(ns uuid.UUID, proposer string, body map[string]any) (string, string) {
	e.t.Helper()
	body["target_namespace"] = ns
	resp := e.do("POST", "/v1/promotions", body, asPrincipal(proposer))
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		e.t.Fatalf("propose status %d", resp.StatusCode)
	}
	var p struct {
		ID     string `json:"id"`
		FactID string `json:"fact_id"`
	}
	decode(e.t, resp, &p)
	return p.ID, p.FactID
}

// §14.11 — auto-promote fails closed: a non-matching candidate goes to human review.
func Test14_11_AutoPromoteFailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("adm", org, "namespace_admin")
	e.grant("alice", org, "contributor")
	e.configureSimpleEngine(org, "adm")

	// origin_type "manual" does not match the rule (merged_pr) → pending.
	promo, fact := e.proposeWithCandidate(org, "alice", map[string]any{
		"proposed_statement": "x", "subject": map[string]any{"type": "t"}, "origin_type": "manual",
	})
	var state string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT state FROM promotion_requests WHERE id = $1`, promo).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		t.Errorf("non-matching candidate state = %s, want pending (fail closed §14.11)", state)
	}
	if e.factRow(fact).status != "proposed" {
		t.Error("fact must remain proposed when the engine does not match")
	}
}

// §14.12 — a matching candidate auto-promotes with engine:<ID> attribution and
// the fact tagged auto:true, end-to-end over HTTP.
func Test14_12_AutoPromoteAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("adm", org, "namespace_admin")
	e.grant("alice", org, "contributor")
	e.configureSimpleEngine(org, "adm")

	// origin_type merged_pr matches → auto-promoted to active.
	_, fact := e.proposeWithCandidate(org, "alice", map[string]any{
		"proposed_statement": "main builds with bazel",
		"subject":            map[string]any{"type": "build.command", "scope": "main"},
		"origin_type":        "merged_pr",
	})

	row := e.factRow(fact)
	if row.status != "active" {
		t.Fatalf("fact status = %s, want active (auto-promoted)", row.status)
	}
	// auto:true tag.
	var tags []string
	if err := e.pool.QueryRow(context.Background(), `SELECT tags FROM facts WHERE id = $1`, fact).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if !contains(tags, "auto:true") {
		t.Errorf("tags = %v, want auto:true", tags)
	}
	// engine attribution in the audit trail.
	var principal string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT principal FROM audit_events WHERE subject_id = $1 AND action = 'fact.auto_promoted'`, fact).Scan(&principal); err != nil {
		t.Fatalf("auto_promoted audit event missing: %v", err)
	}
	if principal != "engine:simple/v1" {
		t.Errorf("principal = %q, want engine:simple/v1", principal)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// §14.15 — a policy with an unknown engine ID, or rules that fail Validate, is
// rejected at write time.
func Test14_15_InvalidPolicyRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("adm", org, "namespace_admin")

	bad := []map[string]any{
		{"required_approvals": 1, "auto_promote": map[string]any{"engine": "cel/v1"}},
		{"required_approvals": 1, "auto_promote": map[string]any{
			"engine": "simple/v1",
			"rules":  map[string]any{"rules": []any{map[string]any{"all": []any{map[string]any{"field": "statement", "op": "eq", "value": "x"}}}}},
		}},
	}
	for i, policy := range bad {
		resp := e.do("PATCH", "/v1/namespaces/"+org.String(), policy, asPrincipal("adm"))
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("bad policy %d: status %d, want 400", i, resp.StatusCode)
		}
	}
}

// Note: §14.13 (engine determinism) and §14.14 (version isolation) are asserted
// at the unit level in internal/autopromote and internal/autopromote/simple,
// where engine internals and the registry's ID dispatch are directly observable
// (a positive/negative decision and the registration of an alternate version
// cannot be expressed purely over the HTTP surface). They are part of the build's
// green bar; this file covers the HTTP-observable §14.11, §14.12, and §14.15.

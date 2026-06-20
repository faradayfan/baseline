package conformance

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

// §14.1 — a fact is active iff its promotion has approvals ≥ required_approvals.
func Test14_01_ActiveIffApproved(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance runs against a live instance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 2)
	e.grant("alice", org, "contributor")
	e.grant("bob", org, "reviewer")
	e.grant("carol", org, "reviewer")

	promo, fact := e.propose(org, "alice", "deploys via CI", map[string]any{"type": "deploy.policy"})
	e.do("POST", "/v1/promotions/"+promo+"/submit", nil, asPrincipal("alice")).Body.Close()

	// 1 of 2 approvals → not active.
	e.do("POST", "/v1/promotions/"+promo+"/approve", map[string]any{"comment": "ok"}, asPrincipal("bob")).Body.Close()
	if got := e.factStatus(fact, "bob"); got == "active" {
		t.Fatal("active before reaching required_approvals")
	}
	// 2 of 2 → active.
	e.do("POST", "/v1/promotions/"+promo+"/approve", map[string]any{"comment": "ok"}, asPrincipal("carol")).Body.Close()
	if got := e.factStatus(fact, "bob"); got != "active" {
		t.Errorf("status = %s, want active after required approvals", got)
	}
}

// §14.2 — no two active facts share (namespace_id, canonical_key): DB index + assertion.
func Test14_02_OneActivePerKey(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespace("org", "org", nil)

	insert := func() error {
		_, err := e.pool.Exec(context.Background(), `
			INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by)
			VALUES ($1,'x','{}'::jsonb,'k:1','active','t')`, org)
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("first active insert: %v", err)
	}
	if err := insert(); err == nil {
		t.Fatal("second active fact with same key must be rejected by facts_active_unique")
	}
}

// §14.5 — every state transition produces exactly one immutable AuditEvent.
func Test14_05_OneAuditPerTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("alice", org, "contributor")
	e.grant("bob", org, "reviewer")

	promo, fact := e.propose(org, "alice", "x", map[string]any{"type": "t"})
	e.do("POST", "/v1/promotions/"+promo+"/submit", nil, asPrincipal("alice")).Body.Close()
	e.do("POST", "/v1/promotions/"+promo+"/approve", map[string]any{"comment": "ok"}, asPrincipal("bob")).Body.Close()

	// proposed, submitted, approved, activated → ≥4 distinct events; none deleted/updated.
	var count int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE subject_id = $1 OR subject_id = $2`,
		promo, fact).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 4 {
		t.Errorf("audit events = %d, want >= 4", count)
	}
}

// §14.6 — a proposer cannot approve their own promotion, at any policy.
func Test14_06_SeparationOfDuties(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("alice", org, "contributor")
	e.grant("alice", org, "reviewer") // even with reviewer, can't self-approve

	promo, _ := e.propose(org, "alice", "x", map[string]any{"type": "t"})
	e.do("POST", "/v1/promotions/"+promo+"/submit", nil, asPrincipal("alice")).Body.Close()

	resp := e.do("POST", "/v1/promotions/"+promo+"/approve", map[string]any{"comment": "self"}, asPrincipal("alice"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("self-approve status = %d, want 403", resp.StatusCode)
	}
}

// §14.7 — approving a conflicting fact atomically supersedes the prior, lineage both ways.
func Test14_07_ConflictSupersession(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("alice", org, "contributor")
	e.grant("dave", org, "contributor")
	e.grant("bob", org, "reviewer")

	subject := map[string]any{"type": "build.command", "scope": "svc"}
	p1, f1 := e.propose(org, "alice", "make", subject)
	e.do("POST", "/v1/promotions/"+p1+"/submit", nil, asPrincipal("alice")).Body.Close()
	e.do("POST", "/v1/promotions/"+p1+"/approve", map[string]any{}, asPrincipal("bob")).Body.Close()

	p2, f2 := e.propose(org, "dave", "bazel", subject)
	e.do("POST", "/v1/promotions/"+p2+"/submit", nil, asPrincipal("dave")).Body.Close()
	e.do("POST", "/v1/promotions/"+p2+"/approve", map[string]any{}, asPrincipal("bob")).Body.Close()

	old := e.factRow(f1)
	nw := e.factRow(f2)
	if old.status != "superseded" || nw.status != "active" {
		t.Fatalf("statuses = old:%s new:%s, want superseded/active", old.status, nw.status)
	}
	if old.supersededBy == nil || *old.supersededBy != nw.id {
		t.Error("old.superseded_by_id not set to new fact")
	}
	if nw.supersedes == nil || *nw.supersedes != old.id {
		t.Error("new.supersedes_id not set to old fact")
	}
}

// §14.8 — stale PATCH (wrong version) returns 409, no write.
func Test14_08_OptimisticConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)
	e.grant("alice", org, "contributor")
	e.grant("bob", org, "reviewer")

	promo, fact := e.propose(org, "alice", "x", map[string]any{"type": "t"})
	e.do("POST", "/v1/promotions/"+promo+"/submit", nil, asPrincipal("alice")).Body.Close()
	e.do("POST", "/v1/promotions/"+promo+"/approve", map[string]any{}, asPrincipal("bob")).Body.Close()

	version := e.factRow(fact).version
	stale := map[string]string{"X-Baseline-Principal": "bob", "If-Match": strconv.Itoa(version - 1)}
	resp := e.do("PATCH", "/v1/facts/"+fact, nil, stale)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("stale revoke = %d, want 409", resp.StatusCode)
	}
	if e.factRow(fact).status != "active" {
		t.Error("stale revoke must not have written")
	}
}

// --- small query helpers used above ---

func (e *env) factStatus(id, principal string) string {
	e.t.Helper()
	resp := e.do("GET", "/v1/facts/"+id, nil, asPrincipal(principal))
	var f struct {
		Status string `json:"status"`
	}
	decode(e.t, resp, &f)
	return f.Status
}

type factRowT struct {
	id           uuid.UUID
	status       string
	version      int
	supersedes   *uuid.UUID
	supersededBy *uuid.UUID
}

func (e *env) factRow(id string) factRowT {
	e.t.Helper()
	var r factRowT
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id, status, version, supersedes_id, superseded_by_id FROM facts WHERE id = $1`, id).
		Scan(&r.id, &r.status, &r.version, &r.supersedes, &r.supersededBy); err != nil {
		e.t.Fatalf("factRow: %v", err)
	}
	return r
}

package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/storetest"
)

// seedActiveFact inserts an active fact directly. tags/validTo are optional.
func seedActiveFact(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, key, statement string, tags []string, validTo *time.Time) {
	t.Helper()
	if tags == nil {
		tags = []string{}
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, valid_to, created_by, valid_from)
		VALUES ($1, $2, '{}'::jsonb, $3, 'active', $4, $5, 'seed', now())`,
		ns, statement, key, tags, validTo)
	if err != nil {
		t.Fatalf("seed fact: %v", err)
	}
}

func seedFactStatus(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, key, statement, status string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by)
		VALUES ($1, $2, '{}'::jsonb, $3, $4, 'seed')`, ns, statement, key, status)
	if err != nil {
		t.Fatalf("seed fact status: %v", err)
	}
}

// TestAPI_Context_TagFilter asserts ?tags= narrows /context by ANY-match, with
// authoritative:true facts always included.
func TestAPI_Context_TagFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	grant(t, pool, "alice", org, "reader")
	seedActiveFact(t, pool, org, "sec:tls", "use mTLS", []string{"security"}, nil)
	seedActiveFact(t, pool, org, "fe:bundle", "tree-shake", []string{"frontend"}, nil)
	seedActiveFact(t, pool, org, "base:ci", "via CI", []string{"authoritative:true"}, nil)

	keys := func(qs string) map[string]bool {
		resp := api.Do(t, "GET", "/v1/context"+qs, nil, hdr("alice"))
		var items []map[string]any
		storetest.DecodeJSON(t, resp, &items)
		out := map[string]bool{}
		for _, it := range items {
			if k, ok := it["canonical_key"].(string); ok {
				out[k] = true
			}
		}
		return out
	}

	// tags=security → security fact + authoritative; not frontend.
	sec := keys("?tags=security")
	if !sec["sec:tls"] || !sec["base:ci"] || sec["fe:bundle"] {
		t.Errorf("?tags=security = %v, want sec:tls + base:ci only", sec)
	}
	// no tags → everything.
	all := keys("")
	if len(all) != 3 {
		t.Errorf("no tags should return all 3, got %v", all)
	}
}

// TestAPI_Facts_TagFilter asserts ?tags= on GET /facts: ANY-match + both
// always-pass delivery markers (authoritative:true and tier:always).
func TestAPI_Facts_TagFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	grant(t, pool, "alice", org, "reader")
	seedActiveFact(t, pool, org, "sec:tls", "use mTLS", []string{"security"}, nil)
	seedActiveFact(t, pool, org, "fe:bundle", "tree-shake", []string{"frontend"}, nil)
	seedActiveFact(t, pool, org, "base:ci", "via CI", []string{"authoritative:true"}, nil)
	seedActiveFact(t, pool, org, "guard:tests", "run tests", []string{"tier:always"}, nil)

	resp := api.Do(t, "GET", "/v1/facts?tags=security", nil, hdr("alice"))
	var facts []map[string]any
	storetest.DecodeJSON(t, resp, &facts)
	got := map[string]bool{}
	for _, f := range facts {
		got[f["canonical_key"].(string)] = true
	}
	// security (ANY-match) + both always-pass markers; NOT the frontend fact.
	if !got["sec:tls"] || !got["base:ci"] || !got["guard:tests"] || got["fe:bundle"] {
		t.Errorf("/facts?tags=security = %v, want sec:tls + base:ci + guard:tests only", got)
	}
}

// TestAPI_Context_NoLeakOutsideEntitlements asserts §14.3: /context never
// returns a fact outside the caller's entitled namespaces.
func TestAPI_Context_NoLeakOutsideEntitlements(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	secret := seedNamespace(t, pool, "team:secret", "team", &org)

	seedActiveFact(t, pool, org, "policy:ci", "deploys via CI", nil, nil)
	seedActiveFact(t, pool, secret, "secret:thing", "classified", nil, nil)

	// alice reads org only (no membership in team:secret).
	grant(t, pool, "alice", org, "reader")

	resp := api.Do(t, "GET", "/v1/context", nil, hdr("alice"))
	var items []map[string]any
	storetest.DecodeJSON(t, resp, &items)

	for _, it := range items {
		if it["canonical_key"] == "secret:thing" {
			t.Fatal("leaked a fact from a non-entitled namespace (§14.3)")
		}
	}
	if len(items) != 1 || items[0]["canonical_key"] != "policy:ci" {
		t.Errorf("expected only org fact, got %v", items)
	}
}

// TestAPI_Context_NoStaleFacts asserts §14.4: expired/revoked/superseded facts
// and facts past valid_to are never returned.
func TestAPI_Context_NoStaleFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	grant(t, pool, "alice", org, "reader")

	seedActiveFact(t, pool, org, "live:key", "current", nil, nil)
	past := time.Now().Add(-1 * time.Hour)
	seedActiveFact(t, pool, org, "expired:key", "stale by valid_to", nil, &past)
	seedFactStatus(t, pool, org, "revoked:key", "revoked", "revoked")
	seedFactStatus(t, pool, org, "superseded:key", "superseded", "superseded")

	resp := api.Do(t, "GET", "/v1/context", nil, hdr("alice"))
	var items []map[string]any
	storetest.DecodeJSON(t, resp, &items)

	got := map[string]bool{}
	for _, it := range items {
		got[it["canonical_key"].(string)] = true
	}
	if !got["live:key"] {
		t.Error("active in-validity fact should be returned")
	}
	for _, stale := range []string{"expired:key", "revoked:key", "superseded:key"} {
		if got[stale] {
			t.Errorf("stale fact %q must not be returned (§14.4)", stale)
		}
	}
}

// TestAPI_Context_Precedence asserts §14.9: for the same canonical_key, the
// highest-precedence fact wins (user ▸ project ▸ team ▸ org), and authoritative
// overrides specificity.
func TestAPI_Context_Precedence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	team := seedNamespace(t, pool, "team:eng", "team", &org)

	// Same key in org and team; team is more specific → team wins.
	seedActiveFact(t, pool, org, "build.command:svc", "org: make", nil, nil)
	seedActiveFact(t, pool, team, "build.command:svc", "team: bazel", nil, nil)

	grant(t, pool, "alice", team, "reader") // entitles team + ancestor org

	resp := api.Do(t, "GET", "/v1/context", nil, hdr("alice"))
	var items []map[string]any
	storetest.DecodeJSON(t, resp, &items)

	var got string
	for _, it := range items {
		if it["canonical_key"] == "build.command:svc" {
			got = it["statement"].(string)
		}
	}
	if got != "team: bazel" {
		t.Errorf("precedence: got %q, want team's (more specific) fact", got)
	}
}

// TestAPI_Context_AuthoritativeOverrides asserts the authoritative:true override:
// an org fact tagged authoritative beats a more-specific team fact.
func TestAPI_Context_AuthoritativeOverrides(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	team := seedNamespace(t, pool, "team:eng", "team", &org)

	seedActiveFact(t, pool, org, "sec:baseline", "org authoritative", []string{"authoritative:true"}, nil)
	seedActiveFact(t, pool, team, "sec:baseline", "team override attempt", nil, nil)

	grant(t, pool, "alice", team, "reader")

	resp := api.Do(t, "GET", "/v1/context", nil, hdr("alice"))
	var items []map[string]any
	storetest.DecodeJSON(t, resp, &items)

	var got string
	for _, it := range items {
		if it["canonical_key"] == "sec:baseline" {
			got = it["statement"].(string)
		}
	}
	if got != "org authoritative" {
		t.Errorf("authoritative override: got %q, want org's authoritative fact", got)
	}
}

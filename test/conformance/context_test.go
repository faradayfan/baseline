package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (e *env) seedActiveFact(ns uuid.UUID, key, statement string, tags []string, validTo *time.Time) {
	e.t.Helper()
	if tags == nil {
		tags = []string{}
	}
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, valid_to, created_by, valid_from)
		VALUES ($1,$2,'{}'::jsonb,$3,'active',$4,$5,'seed',now())`, ns, statement, key, tags, validTo); err != nil {
		e.t.Fatalf("seed fact: %v", err)
	}
}

func (e *env) seedFactStatus(ns uuid.UUID, key, status string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, created_by)
		VALUES ($1,'s','{}'::jsonb,$2,$3,'seed')`, ns, key, status); err != nil {
		e.t.Fatalf("seed: %v", err)
	}
}

func (e *env) contextKeys(principal string) map[string]string {
	e.t.Helper()
	resp := e.do("GET", "/v1/context", nil, asPrincipal(principal))
	var items []struct {
		CanonicalKey string `json:"canonical_key"`
		Statement    string `json:"statement"`
	}
	decode(e.t, resp, &items)
	out := map[string]string{}
	for _, it := range items {
		out[it.CanonicalKey] = it.Statement
	}
	return out
}

// §14.3 — /context never returns a fact outside the caller's entitled namespaces.
func Test14_03_ContextNoLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespace("org", "org", nil)
	secret := e.seedNamespace("team:secret", "team", &org)
	e.seedActiveFact(org, "ok:key", "visible", nil, nil)
	e.seedActiveFact(secret, "secret:key", "hidden", nil, nil)
	e.grant("alice", org, "reader") // not a member of team:secret

	keys := e.contextKeys("alice")
	if _, leaked := keys["secret:key"]; leaked {
		t.Error("leaked a fact outside entitlements (§14.3)")
	}
	if _, ok := keys["ok:key"]; !ok {
		t.Error("entitled fact missing")
	}
}

// §14.4 — /context never returns expired/revoked/superseded or past-valid_to facts.
func Test14_04_ContextNoStale(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespace("org", "org", nil)
	e.grant("alice", org, "reader")

	e.seedActiveFact(org, "live", "ok", nil, nil)
	past := time.Now().Add(-time.Hour)
	e.seedActiveFact(org, "expired_by_time", "stale", nil, &past)
	e.seedFactStatus(org, "revoked_k", "revoked")
	e.seedFactStatus(org, "superseded_k", "superseded")
	e.seedFactStatus(org, "expired_k", "expired")

	keys := e.contextKeys("alice")
	if _, ok := keys["live"]; !ok {
		t.Error("active in-validity fact should appear")
	}
	for _, stale := range []string{"expired_by_time", "revoked_k", "superseded_k", "expired_k"} {
		if _, bad := keys[stale]; bad {
			t.Errorf("stale fact %q must not appear (§14.4)", stale)
		}
	}
}

// §14.9 — precedence: highest-precedence active fact per key; authoritative overrides.
func Test14_09_Precedence(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespace("org", "org", nil)
	team := e.seedNamespace("team:eng", "team", &org)
	e.grant("alice", team, "reader") // entitles team + ancestor org

	// More-specific team wins for the same key.
	e.seedActiveFact(org, "build:svc", "org-make", nil, nil)
	e.seedActiveFact(team, "build:svc", "team-bazel", nil, nil)
	// authoritative org fact overrides a more-specific team fact.
	e.seedActiveFact(org, "sec:base", "org-auth", []string{"authoritative:true"}, nil)
	e.seedActiveFact(team, "sec:base", "team-attempt", nil, nil)

	keys := e.contextKeys("alice")
	if keys["build:svc"] != "team-bazel" {
		t.Errorf("precedence: build:svc = %q, want team-bazel", keys["build:svc"])
	}
	if keys["sec:base"] != "org-auth" {
		t.Errorf("authoritative override: sec:base = %q, want org-auth", keys["sec:base"])
	}
}

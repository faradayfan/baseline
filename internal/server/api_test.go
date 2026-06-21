package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/storetest"
)

// newAPI builds the real server handler (HeaderAuthenticator) over a fresh DB.
func newAPI(t *testing.T) (*storetest.API, *pgxpool.Pool) {
	t.Helper()
	h := storetest.Shared(t)
	var pool *pgxpool.Pool
	api := storetest.NewAPI(t, h, func(p *pgxpool.Pool) http.Handler {
		pool = p
		return server.New(p, server.HeaderAuthenticator{}).Handler()
	})
	return api, pool
}

// hdr builds principal headers for a request.
func hdr(principal string) map[string]string {
	return map[string]string{"X-Baseline-Principal": principal}
}

func adminHdr(principal string) map[string]string {
	return map[string]string{
		"X-Baseline-Principal":      principal,
		"X-Baseline-Platform-Admin": "true",
	}
}

// seedNamespace inserts a namespace directly and returns its id.
func seedNamespace(t *testing.T, pool *pgxpool.Pool, name, kind string, parent *uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind, parent_id) VALUES ($1,$2,$3) RETURNING id`,
		name, kind, parent).Scan(&id)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	return id
}

func grant(t *testing.T, pool *pgxpool.Pool, principal string, ns uuid.UUID, role string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO memberships (principal, namespace_id, role) VALUES ($1,$2,$3)`, principal, ns, role)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
}

func TestAPI_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, _ := newAPI(t)

	// No principal header → 401.
	resp := api.Do(t, "GET", "/v1/namespaces", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAPI_Healthz_NoAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, _ := newAPI(t)
	resp := api.Do(t, "GET", "/healthz", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
}

func TestAPI_Readyz_NoAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, _ := newAPI(t)
	resp := api.Do(t, "GET", "/readyz", nil, nil)
	defer resp.Body.Close()
	// DB is reachable in the test env, so readiness is 200.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz status = %d, want 200", resp.StatusCode)
	}
}

func TestAPI_CreateNamespace_PlatformAdminOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, _ := newAPI(t)
	body := map[string]any{"name": "org", "kind": "org"}

	// Non-admin → 403.
	resp := api.Do(t, "POST", "/v1/namespaces", body, hdr("alice"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin create status = %d, want 403", resp.StatusCode)
	}

	// Platform admin → 201.
	resp = api.Do(t, "POST", "/v1/namespaces", body, adminHdr("root"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("admin create status = %d, want 201", resp.StatusCode)
	}
}

// TestAPI_RBACMatrix_EndToEnd exercises the §7.2 matrix through real endpoints,
// positively and negatively (conformance §14.10, API half).
func TestAPI_RBACMatrix_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)

	// read facts (getNamespace): reader can, stranger cannot.
	grant(t, pool, "reader1", org, "reader")
	resp := api.Do(t, "GET", "/v1/namespaces/"+org.String(), nil, hdr("reader1"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("reader GET ns = %d, want 200", resp.StatusCode)
	}
	resp = api.Do(t, "GET", "/v1/namespaces/"+org.String(), nil, hdr("stranger"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("stranger GET ns = %d, want 403", resp.StatusCode)
	}

	// manage members (addMember): admin can, reader cannot.
	addBody := map[string]any{"principal": "newbie", "role": "contributor"}
	resp = api.Do(t, "POST", "/v1/namespaces/"+org.String()+"/members", addBody, hdr("reader1"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader addMember = %d, want 403", resp.StatusCode)
	}

	grant(t, pool, "admin1", org, "namespace_admin")
	resp = api.Do(t, "POST", "/v1/namespaces/"+org.String()+"/members", addBody, hdr("admin1"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("admin addMember = %d, want 201", resp.StatusCode)
	}

	// The newly-added contributor can now read (was granted via the API).
	resp = api.Do(t, "GET", "/v1/namespaces/"+org.String()+"/members", nil, hdr("newbie"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("contributor listMembers = %d, want 200", resp.StatusCode)
	}

	// invalid role → 400.
	bad := map[string]any{"principal": "x", "role": "god"}
	resp = api.Do(t, "POST", "/v1/namespaces/"+org.String()+"/members", bad, hdr("admin1"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid role = %d, want 400", resp.StatusCode)
	}
}

// TestAPI_ReadInheritance verifies a team member can read the org via the API
// (upward parent-chain inheritance, §6) but not a sibling/descendant.
func TestAPI_ReadInheritance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	team := seedNamespace(t, pool, "team:eng", "team", &org)
	project := seedNamespace(t, pool, "project:x", "project", &team)

	grant(t, pool, "teamie", team, "reader")

	// Can read team and ancestor org.
	for _, ns := range []uuid.UUID{team, org} {
		resp := api.Do(t, "GET", "/v1/namespaces/"+ns.String(), nil, hdr("teamie"))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("teamie read %s = %d, want 200", ns, resp.StatusCode)
		}
	}
	// Cannot read descendant project.
	resp := api.Do(t, "GET", "/v1/namespaces/"+project.String(), nil, hdr("teamie"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("teamie read project = %d, want 403", resp.StatusCode)
	}
}

// TestAPI_ListNamespaces_ScopedToEntitlements ensures list never leaks a
// namespace the caller can't read.
func TestAPI_ListNamespaces_ScopedToEntitlements(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := seedNamespace(t, pool, "org", "org", nil)
	_ = seedNamespace(t, pool, "team:secret", "team", &org) // not readable by alice

	grant(t, pool, "alice", org, "reader")

	resp := api.Do(t, "GET", "/v1/namespaces", nil, hdr("alice"))
	var got []map[string]any
	storetest.DecodeJSON(t, resp, &got)
	if len(got) != 1 {
		t.Fatalf("alice sees %d namespaces, want 1 (only org)", len(got))
	}
	if got[0]["name"] != "org" {
		t.Errorf("alice sees %v, want org", got[0]["name"])
	}
}

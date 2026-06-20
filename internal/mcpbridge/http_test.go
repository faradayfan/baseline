package mcpbridge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/faradayfan/baseline/internal/mcpbridge"
	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/storetest"
)

// headerRoundTripper injects a fixed X-Baseline-Principal on every request,
// simulating how a remote Claude client carries identity over the HTTP transport.
type headerRoundTripper struct {
	principal string
	base      http.RoundTripper
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(mcpbridge.PrincipalHeader, h.principal)
	return h.base.RoundTrip(req)
}

// connectHTTP dials the MCP-over-HTTP endpoint as `principal`.
func connectHTTP(t *testing.T, endpoint, principal string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: headerRoundTripper{principal: principal, base: http.DefaultTransport}},
	}
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect as %s: %v", principal, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestHTTP_PerRequestPrincipalIsolation is the security-relevant test for the
// remote transport: two clients with different X-Baseline-Principal headers,
// hitting the SAME /mcp endpoint, must each resolve to their OWN entitlements —
// neither sees the other's namespace.
func TestHTTP_PerRequestPrincipalIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	h := storetest.Shared(t)
	pool := h.FreshDB(t)
	restHandler := server.New(pool, server.HeaderAuthenticator{}).Handler()

	// Two namespaces, each readable by exactly one principal.
	nsA := seedNS(t, pool, "team:a", "team")
	nsB := seedNS(t, pool, "team:b", "team")
	grant(t, pool, "alice", nsA, "reader")
	grant(t, pool, "bob", nsB, "reader")
	seedFactIn(t, pool, nsA, "a:key", "alice's fact")
	seedFactIn(t, pool, nsB, "b:key", "bob's fact")

	// One shared HTTP MCP endpoint — exactly the hosted topology.
	srv := httptest.NewServer(mcpbridge.HTTPHandler(restHandler))
	t.Cleanup(srv.Close)
	endpoint := srv.URL + "/mcp"

	aliceCtx := e2eContext(t, connectHTTP(t, endpoint, "alice"))
	bobCtx := e2eContext(t, connectHTTP(t, endpoint, "bob"))

	// Alice sees only a:key; bob sees only b:key.
	if !containsStr(aliceCtx, "a:key") || containsStr(aliceCtx, "b:key") {
		t.Errorf("alice leaked or missing: %q", aliceCtx)
	}
	if !containsStr(bobCtx, "b:key") || containsStr(bobCtx, "a:key") {
		t.Errorf("bob leaked or missing: %q", bobCtx)
	}
}

// TestHTTP_NoPrincipalIsUnauthenticated confirms a request without the header is
// rejected (the tool call surfaces the REST 401 as an error) — no anonymous access.
func TestHTTP_NoPrincipalIsUnauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	h := storetest.Shared(t)
	pool := h.FreshDB(t)
	restHandler := server.New(pool, server.HeaderAuthenticator{}).Handler()

	srv := httptest.NewServer(mcpbridge.HTTPHandler(restHandler))
	t.Cleanup(srv.Close)

	cs := connectHTTP(t, srv.URL+"/mcp", "") // empty principal
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_context", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Error("a request with no principal must be rejected (401 surfaced as tool error)")
	}
}

// --- helpers ---

func seedFactIn(t *testing.T, pool *pgxpool.Pool, ns uuid.UUID, key, statement string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
		VALUES ($1,$2,'{}'::jsonb,$3,'active','{}','seed',now())`, ns, statement, key); err != nil {
		t.Fatalf("seed fact: %v", err)
	}
}

// e2eContext calls get_context and returns the text body.
func e2eContext(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_context", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("get_context: %v", err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

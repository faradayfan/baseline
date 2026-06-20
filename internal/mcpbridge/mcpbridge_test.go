package mcpbridge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/faradayfan/baseline/internal/mcpbridge"
	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/storetest"
)

// connect builds a bridge for `principal` over a fresh DB and returns a connected
// MCP client session plus the pool for seeding.
func connect(t *testing.T, principal string) (*mcp.ClientSession, *pgxpool.Pool) {
	t.Helper()
	h := storetest.Shared(t)
	pool := h.FreshDB(t)
	handler := server.New(pool, server.HeaderAuthenticator{}).Handler()

	bridge := mcpbridge.New(handler, principal)
	srv := bridge.Server()

	clientT, serverT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, pool
}

func seedNS(t *testing.T, pool *pgxpool.Pool, name, kind string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind) VALUES ($1,$2) RETURNING id`, name, kind).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func grant(t *testing.T, pool *pgxpool.Pool, principal string, ns uuid.UUID, role string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO memberships (principal, namespace_id, role) VALUES ($1,$2,$3)`, principal, ns, role); err != nil {
		t.Fatal(err)
	}
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	return res
}

// TestTools_Listed confirms all five §9 tools are registered.
func TestTools_Listed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, _ := connect(t, "alice")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"get_context": false, "search_facts": false, "propose_fact": false,
		"list_my_promotions": false, "review_promotion": false,
	}
	for _, tool := range res.Tools {
		want[tool.Name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q not registered", name)
		}
	}
}

// TestProposeFact_MapsToREST asserts propose_fact creates a promotion via the
// REST path, reusing RBAC (contributor required).
func TestProposeFact_MapsToREST(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "alice")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "alice", org, "contributor")

	res := call(t, cs, "propose_fact", map[string]any{
		"target_namespace":   org.String(),
		"proposed_statement": "deploys go through CI",
		"subject":            map[string]any{"type": "deploy.policy"},
	})
	if res.IsError {
		t.Fatalf("propose_fact errored: %v", res.Content)
	}

	// The result wraps the REST 201 body; confirm a promotion now exists.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM promotion_requests WHERE proposer = 'alice'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("promotions for alice = %d, want 1", count)
	}
}

// TestProposeFact_WithTags asserts the propose_fact tool carries tags through to
// the created fact — facts can be authored fully (with their read-path labels)
// via the governed MCP path, no out-of-band SQL.
func TestProposeFact_WithTags(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "alice")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "alice", org, "contributor")

	res := call(t, cs, "propose_fact", map[string]any{
		"target_namespace":   org.String(),
		"proposed_statement": "use asdf for language versions",
		"subject":            map[string]any{"type": "tooling.asdf"},
		"tags":               []string{"tooling", "languages"},
	})
	if res.IsError {
		t.Fatalf("propose_fact errored: %v", res.Content)
	}

	var tags []string
	if err := pool.QueryRow(context.Background(),
		`SELECT tags FROM facts WHERE created_by='alice' AND canonical_key='tooling.asdf:global'`).Scan(&tags); err != nil {
		t.Fatalf("fact lookup: %v", err)
	}
	want := map[string]bool{"tooling": true, "languages": true}
	if len(tags) != 2 || !want[tags[0]] || !want[tags[1]] {
		t.Errorf("proposed fact tags = %v, want [tooling languages]", tags)
	}
}

// TestProposeFact_RBACReused asserts a reader (no propose right) is rejected by
// the same RBAC the REST layer enforces — surfaced as a tool error.
func TestProposeFact_RBACReused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "ron")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "ron", org, "reader") // not a contributor

	res := call(t, cs, "propose_fact", map[string]any{
		"target_namespace":   org.String(),
		"proposed_statement": "x",
		"subject":            map[string]any{"type": "t"},
	})
	if !res.IsError {
		t.Error("reader proposing should be a tool error (403 reused from REST)")
	}
}

// TestGetContext_MapsToREST asserts get_context returns the caller's entitled
// facts through the bridge.
func TestGetContext_MapsToREST(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "alice")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "alice", org, "reader")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
		VALUES ($1,'deploys via CI','{}'::jsonb,'policy:ci','active','{}','seed',now())`, org); err != nil {
		t.Fatal(err)
	}

	res := call(t, cs, "get_context", map[string]any{})
	if res.IsError {
		t.Fatalf("get_context errored: %v", res.Content)
	}
	if !containsStr(bodyText(t, res), "policy:ci") {
		t.Errorf("expected the entitled fact in context, got %s", bodyText(t, res))
	}
}

// TestGetContext_TagFilter asserts the `tags` tool arg narrows get_context over
// the wire (ANY-match), with authoritative facts always passing.
func TestGetContext_TagFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "alice")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "alice", org, "reader")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
		VALUES
		  ($1,'use mTLS','{}'::jsonb,'sec:tls','active','{security}','seed',now()),
		  ($1,'tree-shake','{}'::jsonb,'fe:bundle','active','{frontend}','seed',now()),
		  ($1,'via CI','{}'::jsonb,'base:ci','active','{authoritative:true}','seed',now())`, org); err != nil {
		t.Fatal(err)
	}

	res := call(t, cs, "get_context", map[string]any{"tags": "security"})
	if res.IsError {
		t.Fatalf("get_context errored: %v", res.Content)
	}
	body := bodyText(t, res)
	if !containsStr(body, "sec:tls") || !containsStr(body, "base:ci") {
		t.Errorf("tags=security should include sec:tls + base:ci, got %s", body)
	}
	if containsStr(body, "fe:bundle") {
		t.Errorf("tags=security must NOT include fe:bundle, got %s", body)
	}
}

// TestStructuredContent_Populated guards the rendering fix: a successful tool
// result must carry the parsed body in StructuredContent under a {"result": ...}
// envelope (not just in text content). Regression test for the empty-{} bug.
func TestStructuredContent_Populated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "alice")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "alice", org, "reader")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, created_by, valid_from)
		VALUES ($1,'deploys via CI','{}'::jsonb,'policy:ci','active','{}','seed',now())`, org); err != nil {
		t.Fatal(err)
	}

	res := call(t, cs, "search_facts", map[string]any{})
	if res.IsError {
		t.Fatalf("search_facts errored: %v", res.Content)
	}

	// StructuredContent must be a non-nil object with a "result" key.
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent is %T, want map[string]any (the {result:...} envelope)", res.StructuredContent)
	}
	result, ok := sc["result"]
	if !ok {
		t.Fatalf("StructuredContent missing 'result' key: %v", sc)
	}
	// result is the parsed facts array; confirm it carried the seeded fact.
	arr, ok := result.([]any)
	if !ok || len(arr) == 0 {
		t.Fatalf("result is not a non-empty array: %#v", result)
	}
	first, _ := arr[0].(map[string]any)
	if first["canonical_key"] != "policy:ci" {
		t.Errorf("structured result missing the seeded fact, got %v", first)
	}
}

// TestListMyPromotions_ScopedToCaller asserts list_my_promotions returns only the
// caller's own promotions (proposer=me resolves to the session principal).
func TestListMyPromotions_ScopedToCaller(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	cs, pool := connect(t, "alice")
	org := seedNS(t, pool, "org", "org")
	grant(t, pool, "alice", org, "contributor")
	call(t, cs, "propose_fact", map[string]any{
		"target_namespace": org.String(), "proposed_statement": "x", "subject": map[string]any{"type": "t"},
	})

	res := call(t, cs, "list_my_promotions", map[string]any{})
	if res.IsError {
		t.Fatalf("list_my_promotions errored: %v", res.Content)
	}
	if !containsStr(bodyText(t, res), "alice") {
		t.Errorf("expected alice's promotion, got %s", bodyText(t, res))
	}
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

// bodyText extracts the text content the bridge returns (the verbatim REST body).
func bodyText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

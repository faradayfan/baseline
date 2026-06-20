package mem0_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/faradayfan/baseline/internal/memory"
	"github.com/faradayfan/baseline/internal/memory/mem0"
)

// listResponse mirrors the live OSS server's wrapped shape, verified against the
// running cluster: {"results":[{memory,user_id,created_at,...}], "relations":[...]}.
const listResponse = `{
  "results": [
    {"id":"m1","user_id":"john","memory":"Prefers to deploy on Friday afternoons",
     "metadata":null,"created_at":"2026-06-19T20:52:40.134126-07:00"}
  ],
  "relations": [{"source":"user_id:_john","relationship":"prefers","target":"fridays"}]
}`

// TestList_OSSPathAndUnwrap asserts List hits the unprefixed /memories?user_id=
// path and unwraps {"results":[...]} into neutral memories.
func TestList_OSSPathAndUnwrap(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("user_id")
		_, _ = w.Write([]byte(listResponse))
	}))
	defer srv.Close()

	src := mem0.New(srv.URL, "")
	mems, err := src.List(context.Background(), "john", memory.ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotPath != "/memories" {
		t.Errorf("path = %q, want /memories (no /v1 prefix)", gotPath)
	}
	if gotQuery != "john" {
		t.Errorf("user_id query = %q, want john", gotQuery)
	}
	if len(mems) != 1 {
		t.Fatalf("got %d memories, want 1 (unwrapped from results[])", len(mems))
	}
	if mems[0].Content != "Prefers to deploy on Friday afternoons" || mems[0].ActorID != "john" {
		t.Errorf("memory not mapped: %+v", mems[0])
	}
}

// TestSearch_OSSPath asserts Search posts to /search and unwraps results.
func TestSearch_OSSPath(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(listResponse))
	}))
	defer srv.Close()

	src := mem0.New(srv.URL, "")
	mems, err := src.Search(context.Background(), "john", "deploy", memory.SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/search" {
		t.Errorf("%s %s, want POST /search", gotMethod, gotPath)
	}
	if len(mems) != 1 {
		t.Errorf("got %d, want 1", len(mems))
	}
}

// TestGet_OSSPath asserts Get hits /memories/{id} for a single object.
func TestGet_OSSPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":"m1","user_id":"john","memory":"x","created_at":"2026-06-19T20:52:40Z"}`))
	}))
	defer srv.Close()

	src := mem0.New(srv.URL, "")
	m, err := src.Get(context.Background(), "m1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/memories/m1" {
		t.Errorf("path = %q, want /memories/m1", gotPath)
	}
	if m.ID != "m1" || m.Content != "x" {
		t.Errorf("get not mapped: %+v", m)
	}
}

// TestAdd_OSSPathAndExtract asserts Add POSTs to /memories with the messages/
// user_id shape and returns the first extracted memory from the {results:[...]}
// envelope (Mem0 runs LLM extraction, so the stored text may differ from input).
func TestAdd_OSSPathAndExtract(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		// Mem0 echoes the extracted (rephrased) memory.
		_, _ = w.Write([]byte(`{"results":[{"id":"m9","user_id":"john","memory":"Prefers Friday deploys","created_at":"2026-06-20T00:00:00Z"}]}`))
	}))
	defer srv.Close()

	src := mem0.New(srv.URL, "")
	m, err := src.Add(context.Background(), "john", "I like to deploy on Fridays", memory.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if gotPath != "/memories" {
		t.Errorf("path = %q, want /memories", gotPath)
	}
	if !contains(gotBody, `"user_id":"john"`) || !contains(gotBody, `"role":"user"`) {
		t.Errorf("body missing messages/user_id shape: %s", gotBody)
	}
	// Infer unset → the field is omitted (backend default extraction).
	if contains(gotBody, `"infer"`) {
		t.Errorf("infer should be omitted when AddOpts.Infer is nil, got %s", gotBody)
	}
	if m.ID != "m9" || m.Content != "Prefers Friday deploys" {
		t.Errorf("Add did not return the extracted memory: %+v", m)
	}
}

// TestAdd_InferVerbatim asserts that AddOpts.Infer=false reaches the POST body as
// "infer": false — the verbatim mode that skips Mem0's extraction LLM (the right
// mode for deliberate [remember:] captures).
func TestAdd_InferVerbatim(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"results":[{"id":"m1","user_id":"john","memory":"stored verbatim text","created_at":"2026-06-20T00:00:00Z"}]}`))
	}))
	defer srv.Close()

	verbatim := false
	src := mem0.New(srv.URL, "")
	m, err := src.Add(context.Background(), "john", "stored verbatim text",
		memory.AddOpts{Infer: &verbatim})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !contains(gotBody, `"infer":false`) {
		t.Errorf("body must carry infer:false for verbatim storage, got %s", gotBody)
	}
	if m.Content != "stored verbatim text" {
		t.Errorf("Add returned %+v", m)
	}
}

// TestAdd_DedupNoOpEchoes asserts that when Mem0 extracts nothing (a dedup
// no-op, results:[]), Add echoes the input rather than erroring or returning an
// empty record — the caller gets a clean non-error signal.
func TestAdd_DedupNoOpEchoes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	src := mem0.New(srv.URL, "")
	m, err := src.Add(context.Background(), "john", "already known", memory.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if m.ActorID != "john" || m.Content != "already known" {
		t.Errorf("dedup no-op should echo input, got %+v", m)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestAuthHeader: a configured API key is sent as a Bearer token; an empty key
// sends no Authorization header (the OSS server needs none).
func TestAuthHeader(t *testing.T) {
	cases := []struct {
		key      string
		wantAuth string
	}{
		{"", ""},
		{"secret-key", "Bearer secret-key"},
	}
	for _, tc := range cases {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		src := mem0.New(srv.URL, tc.key)
		_, _ = src.List(context.Background(), "john", memory.ListOpts{})
		srv.Close()
		if gotAuth != tc.wantAuth {
			t.Errorf("key %q: Authorization = %q, want %q", tc.key, gotAuth, tc.wantAuth)
		}
	}
}

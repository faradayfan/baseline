package mem0_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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

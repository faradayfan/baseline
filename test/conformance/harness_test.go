// Package conformance is Baseline's "baseline test" (spec §14): a contract suite
// that asserts all 17 invariants against a fully-wired, live server over HTTP.
// It is intentionally black-box and independent of the per-package tests — a
// build ships v1 only when every test here is green.
//
// Each test names its §14 invariant in its function name and a comment.
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/storetest"
)

func TestMain(m *testing.M) { storetest.Main(m) }

// env is a live Baseline instance for one test: a fresh DB behind the real
// server handler, served over httptest.
type env struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	pool   *pgxpool.Pool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := storetest.Shared(t).FreshDB(t)
	handler := server.New(pool, server.HeaderAuthenticator{}).Handler()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, client: srv.Client(), pool: pool}
}

// do issues an HTTP request as principal (optionally platform admin).
func (e *env) do(method, path string, body any, headers map[string]string) *http.Response {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, r)
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func asPrincipal(p string) map[string]string { return map[string]string{"X-Baseline-Principal": p} }

func asAdmin(p string) map[string]string {
	return map[string]string{"X-Baseline-Principal": p, "X-Baseline-Platform-Admin": "true"}
}

func decode(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// --- direct DB seeding helpers (set up preconditions the API doesn't expose) ---

func (e *env) seedNamespace(name, kind string, parent *uuid.UUID) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind, parent_id) VALUES ($1,$2,$3) RETURNING id`, name, kind, parent).Scan(&id); err != nil {
		e.t.Fatalf("seed namespace: %v", err)
	}
	return id
}

func (e *env) seedNamespaceApprovals(name, kind string, approvals int) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	policy := []byte(`{"required_approvals":` + itoa(approvals) + `}`)
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind, policy) VALUES ($1,$2,$3) RETURNING id`, name, kind, policy).Scan(&id); err != nil {
		e.t.Fatalf("seed namespace: %v", err)
	}
	return id
}

func (e *env) grant(principal string, ns uuid.UUID, role string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO memberships (principal, namespace_id, role) VALUES ($1,$2,$3)`, principal, ns, role); err != nil {
		e.t.Fatalf("grant: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// propose proposes a fact and returns (promotionID, factID).
func (e *env) propose(ns uuid.UUID, proposer, statement string, subject map[string]any) (string, string) {
	e.t.Helper()
	resp := e.do("POST", "/v1/promotions", map[string]any{
		"target_namespace": ns, "proposed_statement": statement, "subject": subject,
	}, asPrincipal(proposer))
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		e.t.Fatalf("propose status %d: %s", resp.StatusCode, b)
	}
	var p struct {
		ID     string `json:"id"`
		FactID string `json:"fact_id"`
	}
	decode(e.t, resp, &p)
	return p.ID, p.FactID
}

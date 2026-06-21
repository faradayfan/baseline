package storetest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// API wraps an httptest.Server fronting the real application handler, plus a
// FreshDB-backed pool, for end-to-end HTTP ("API") tests. Build one per test
// with NewAPI, passing a function that constructs your http.Handler from a pool
// — so the test drives the same router, middleware, and handlers as production.
type API struct {
	Server *httptest.Server
	Client *http.Client
}

// NewAPI creates a fresh database, builds the application handler from it via
// buildHandler, starts an httptest server, and registers teardown.
//
//	api := storetest.NewAPI(t, h, func(p *pgxpool.Pool) http.Handler {
//	    return server.New(p, ...) // your real router
//	})
//	resp := api.Do(t, "GET", "/v1/namespaces", nil, map[string]string{"Authorization": "Bearer ..."})
func NewAPI(t *testing.T, h *Harness, buildHandler func(pool *pgxpool.Pool) http.Handler) *API {
	t.Helper()
	pool := h.FreshDB(t)
	handler := buildHandler(pool)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &API{Server: srv, Client: srv.Client()}
}

// Do issues an HTTP request to the test server. body, if non-nil, is JSON-encoded.
// Returns the response; the caller closes it (or uses DecodeJSON which does).
func (a *API) Do(t *testing.T, method, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("apitest: marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.Server.URL+path, r)
	if err != nil {
		t.Fatalf("apitest: new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		t.Fatalf("apitest: %s %s: %v", method, path, err)
	}
	return resp
}

// DecodeJSON reads and unmarshals a response body into dst, closing the body.
func DecodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("apitest: decode json: %v", err)
	}
}

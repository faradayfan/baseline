package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/memory"
	"github.com/faradayfan/baseline/internal/memory/null"
	"github.com/faradayfan/baseline/internal/server"
	"github.com/faradayfan/baseline/internal/storetest"
)

// fakeWriter is a memory.Source that ALSO implements memory.Writer, capturing the
// last Add call so the test can assert the proxy forwarded actor + content.
type fakeWriter struct {
	null.Source // embeds the read-only no-op source (List/Search/Get)
	lastActor   string
	lastContent string
}

func (f *fakeWriter) Add(_ context.Context, actorID, content string, md map[string]any) (memory.Memory, error) {
	f.lastActor = actorID
	f.lastContent = content
	// Echo a stored memory, as the mem0 adapter would after extraction.
	return memory.Memory{ID: "m-1", ActorID: actorID, Content: content, Metadata: md}, nil
}

// newAPIWithMemory builds the handler with an explicit memory source.
func newAPIWithMemory(t *testing.T, mem memory.Source) (*storetest.API, *pgxpool.Pool) {
	t.Helper()
	h := storetest.Shared(t)
	var pool *pgxpool.Pool
	api := storetest.NewAPI(t, h, func(p *pgxpool.Pool) http.Handler {
		pool = p
		return server.NewWithMemory(p, server.HeaderAuthenticator{}, mem).Handler()
	})
	return api, pool
}

// TestAPI_AddMemory_ForwardsToWriter asserts POST /v1/memories proxies to the
// backend Writer with the caller's principal as the default actor, and returns
// the stored memory (201).
func TestAPI_AddMemory_ForwardsToWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	fw := &fakeWriter{}
	api, _ := newAPIWithMemory(t, fw)

	resp := api.Do(t, "POST", "/v1/memories",
		map[string]any{"content": "deploys happen on Fridays"}, hdr("john"))
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if fw.lastActor != "john" {
		t.Errorf("actor forwarded = %q, want john (defaults to principal)", fw.lastActor)
	}
	if fw.lastContent != "deploys happen on Fridays" {
		t.Errorf("content forwarded = %q", fw.lastContent)
	}

	var got memory.Memory
	storetest.DecodeJSON(t, resp, &got)
	if got.Content != "deploys happen on Fridays" || got.ActorID != "john" {
		t.Errorf("returned memory = %+v", got)
	}
}

// TestAPI_AddMemory_ActorOverride asserts an explicit actor_id overrides the
// principal default.
func TestAPI_AddMemory_ActorOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	fw := &fakeWriter{}
	api, _ := newAPIWithMemory(t, fw)

	resp := api.Do(t, "POST", "/v1/memories",
		map[string]any{"content": "x", "actor_id": "service-bot"}, hdr("john"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if fw.lastActor != "service-bot" {
		t.Errorf("actor = %q, want the explicit override service-bot", fw.lastActor)
	}
}

// TestAPI_AddMemory_NullSource501 asserts the standards-only (null) source — which
// does NOT implement memory.Writer — yields 501, not a crash. This guards the
// §11 boundary: a read-only backend has no capture path.
func TestAPI_AddMemory_NullSource501(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, _ := newAPIWithMemory(t, null.New())

	resp := api.Do(t, "POST", "/v1/memories",
		map[string]any{"content": "x"}, hdr("john"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (null source is read-only)", resp.StatusCode)
	}
}

// TestAPI_AddMemory_EmptyContent400 asserts validation rejects an empty body.
func TestAPI_AddMemory_EmptyContent400(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, _ := newAPIWithMemory(t, &fakeWriter{})

	resp := api.Do(t, "POST", "/v1/memories",
		map[string]any{"content": ""}, hdr("john"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for empty content", resp.StatusCode)
	}
}

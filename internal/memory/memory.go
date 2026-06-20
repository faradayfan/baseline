// Package memory is Baseline's narrow, read-only port to a memory backend
// (§11). The entire dependency is three calls — List, Search, Get — used to
// surface candidate memories for promotion and to merge personal memories into
// /context. Baseline NEVER writes to the backend, and only neutral text +
// metadata crosses the boundary (never embedding vectors), so there is no
// vector-space coupling.
package memory

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get when a memory id does not exist.
var ErrNotFound = errors.New("memory not found")

// Memory is the neutral type that crosses the port boundary (§11). No
// backend-specific shape leaks in.
type Memory struct {
	ID        string         `json:"id"`
	ActorID   string         `json:"actor_id"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// ListOpts bounds a List call.
type ListOpts struct {
	Limit int
}

// SearchOpts bounds a semantic Search call.
type SearchOpts struct {
	Limit int
}

// Source is the entire contract Baseline needs from a memory backend. Mem0 is
// the default adapter; null (standards-only), zep, and letta are others.
//
// Source is READ-ONLY by design (§11): the governance core — fact promotion,
// the /context resolver — never writes to the backend. This is the coupling
// guarantee, and it stays intact.
type Source interface {
	List(ctx context.Context, actorID string, opts ListOpts) ([]Memory, error)
	Search(ctx context.Context, actorID, query string, opts SearchOpts) ([]Memory, error)
	Get(ctx context.Context, id string) (Memory, error)
}

// Writer is a SEPARATE, optional capability for the out-of-band memory-capture
// path (the agent harness adding raw memories), deliberately NOT part of Source.
// The spec puts memory *capture* outside Baseline — Mem0 answers "what has this
// agent seen?", fed by the agent runtime (§1, §11.2). Baseline exposes a thin
// pass-through (POST /v1/memories) only as a harness convenience so a Claude
// Code hook has one URL to hit; the governance read-path does not use this.
//
// An adapter MAY implement Writer; the server type-asserts for it and returns
// 501 when the configured source can't write (e.g. the null source). Keeping it
// off Source means zep/letta/null need not grow a write method.
type Writer interface {
	Add(ctx context.Context, actorID, content string, opts AddOpts) (Memory, error)
}

// AddOpts carries optional write parameters. A struct (rather than positional
// args) so new options don't keep changing the Writer signature.
type AddOpts struct {
	// Metadata is stored alongside the memory (e.g. {"type": "procedural"}).
	Metadata map[string]any
	// Infer controls backend-side extraction. nil → use the backend default
	// (Mem0 extracts/distills). false → store the content VERBATIM (no LLM
	// extraction) — the right mode for deliberate [remember:] captures the caller
	// has already phrased intentionally. true → force extraction.
	Infer *bool
}

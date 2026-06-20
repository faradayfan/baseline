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
type Source interface {
	List(ctx context.Context, actorID string, opts ListOpts) ([]Memory, error)
	Search(ctx context.Context, actorID, query string, opts SearchOpts) ([]Memory, error)
	Get(ctx context.Context, id string) (Memory, error)
}

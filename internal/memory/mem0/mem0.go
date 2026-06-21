// Package mem0 adapts the Mem0 REST API to the memory.Source port (§11). The
// Mem0 REST paths are pinned HERE and nowhere else, so version drift is
// contained to this one package.
//
// This adapter targets the self-hosted Mem0 OSS server (mem0-api-server), whose
// contract — verified live against the running cluster — is:
//   - unprefixed paths: GET /memories?user_id=, POST /search, GET /memories/{id}
//     (the /v1/ prefix is the HOSTED platform at api.mem0.ai, not the OSS server);
//   - list/search responses are wrapped: {"results": [ {memory, user_id, ...} ]};
//   - no auth in the OSS build we run (an optional API key is still sent if set,
//     so the same adapter works against authenticated/hosted deployments).
package mem0

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/faradayfan/baseline/internal/memory"
)

// Source is the Mem0-backed memory source. Selected via MEMORY_SOURCE=mem0.
type Source struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// New builds a Mem0 adapter against baseURL (MEM0_URL). apiKey is optional: when
// non-empty it is sent as a Bearer token (for authenticated/hosted deployments);
// the OSS server we run requires no auth, so "" is fine.
func New(baseURL, apiKey string) Source {
	return Source{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// mem0Memory is the Mem0 wire shape for one memory. Kept private so its fields
// never leak past this adapter; everything returned is the neutral memory.Memory.
type mem0Memory struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	Memory    string         `json:"memory"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
}

// mem0List is the wrapped list/search response: {"results": [...], "relations": [...]}.
// Baseline uses only the flat memories; the graph "relations" are ignored.
type mem0List struct {
	Results []mem0Memory `json:"results"`
}

func (m mem0Memory) toNeutral() memory.Memory {
	return memory.Memory{
		ID: m.ID, ActorID: m.UserID, Content: m.Memory,
		Metadata: m.Metadata, CreatedAt: m.CreatedAt,
	}
}

// List → GET /memories?user_id=
func (s Source) List(ctx context.Context, actorID string, opts memory.ListOpts) ([]memory.Memory, error) {
	q := url.Values{"user_id": {actorID}}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out mem0List
	if err := s.getJSON(ctx, "/memories?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return toNeutralSlice(out.Results), nil
}

// Search → POST /search
func (s Source) Search(ctx context.Context, actorID, query string, opts memory.SearchOpts) ([]memory.Memory, error) {
	body := map[string]any{"user_id": actorID, "query": query}
	if opts.Limit > 0 {
		body["limit"] = opts.Limit
	}
	var out mem0List
	if err := s.postJSON(ctx, "/search", body, &out); err != nil {
		return nil, err
	}
	return toNeutralSlice(out.Results), nil
}

// Add → POST /memories. This is the out-of-band capture path (memory.Writer),
// NOT part of the read-only Source contract — it exists so the agent harness has
// a single Baseline URL to post raw memories to.
//
// With opts.Infer unset/true Mem0 runs LLM extraction on the message (text may be
// rephrased/split/dropped). With opts.Infer == false the content is stored
// VERBATIM — Mem0 skips extraction and embeds the raw text. The response is the
// same {"results":[...]} envelope; we return the first stored memory (or a
// synthetic echo when nothing came back, e.g. a deduped no-op).
//
// NOTE: `infer` requires the patched mem0-api image (deploy/mem0-api) — the stock
// OSS REST `MemoryCreate` model omits the field and silently ignores it.
func (s Source) Add(ctx context.Context, actorID, content string, opts memory.AddOpts) (memory.Memory, error) {
	body := map[string]any{
		"messages": []map[string]string{{"role": "user", "content": content}},
		"user_id":  actorID,
	}
	if len(opts.Metadata) > 0 {
		body["metadata"] = opts.Metadata
	}
	if opts.Infer != nil {
		body["infer"] = *opts.Infer
	}
	var out mem0List
	if err := s.postJSON(ctx, "/memories", body, &out); err != nil {
		return memory.Memory{}, err
	}
	if len(out.Results) == 0 {
		// Mem0 stored/extracted nothing (commonly a dedup no-op). Echo the input so
		// the caller gets a non-error signal rather than a confusing empty record.
		return memory.Memory{ActorID: actorID, Content: content, Metadata: opts.Metadata}, nil
	}
	return out.Results[0].toNeutral(), nil
}

// Get → GET /memories/{id} (returns a single memory object, unwrapped).
func (s Source) Get(ctx context.Context, id string) (memory.Memory, error) {
	var m mem0Memory
	if err := s.getJSON(ctx, "/memories/"+url.PathEscape(id), &m); err != nil {
		return memory.Memory{}, err
	}
	return m.toNeutral(), nil
}

func toNeutralSlice(ms []mem0Memory) []memory.Memory {
	out := make([]memory.Memory, len(ms))
	for i, m := range ms {
		out[i] = m.toNeutral()
	}
	return out
}

func (s Source) getJSON(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	s.auth(req)
	return s.do(req, dst)
}

func (s Source) postJSON(ctx context.Context, path string, body, dst any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	s.auth(req)
	return s.do(req, dst)
}

// auth adds the Bearer token when an API key is configured (no-op otherwise).
func (s Source) auth(req *http.Request) {
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
}

func (s Source) do(req *http.Request, dst any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("mem0: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return memory.ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mem0: %s %s: status %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("mem0: decode: %w", err)
		}
	}
	return nil
}

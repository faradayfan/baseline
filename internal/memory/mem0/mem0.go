// Package mem0 adapts the Mem0 REST API to the memory.Source port (§11). The
// Mem0 REST paths are pinned HERE and nowhere else, so version drift is
// contained to this one package.
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
	client  *http.Client
}

// New builds a Mem0 adapter against baseURL (MEM0_URL).
func New(baseURL string) Source {
	return Source{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// mem0Memory is the Mem0 wire shape. Kept private so its fields never leak past
// this adapter; everything returned is the neutral memory.Memory.
type mem0Memory struct {
	ID        string         `json:"id"`
	UserID    string         `json:"user_id"`
	Memory    string         `json:"memory"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
}

func (m mem0Memory) toNeutral() memory.Memory {
	return memory.Memory{
		ID: m.ID, ActorID: m.UserID, Content: m.Memory,
		Metadata: m.Metadata, CreatedAt: m.CreatedAt,
	}
}

// List → GET /v1/memories?user_id=
func (s Source) List(ctx context.Context, actorID string, opts memory.ListOpts) ([]memory.Memory, error) {
	q := url.Values{"user_id": {actorID}}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out []mem0Memory
	if err := s.getJSON(ctx, "/v1/memories?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return toNeutralSlice(out), nil
}

// Search → POST /v1/memories/search
func (s Source) Search(ctx context.Context, actorID, query string, opts memory.SearchOpts) ([]memory.Memory, error) {
	body := map[string]any{"user_id": actorID, "query": query}
	if opts.Limit > 0 {
		body["limit"] = opts.Limit
	}
	var out []mem0Memory
	if err := s.postJSON(ctx, "/v1/memories/search", body, &out); err != nil {
		return nil, err
	}
	return toNeutralSlice(out), nil
}

// Get → GET /v1/memories/{id}
func (s Source) Get(ctx context.Context, id string) (memory.Memory, error) {
	var m mem0Memory
	if err := s.getJSON(ctx, "/v1/memories/"+url.PathEscape(id), &m); err != nil {
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
	return s.do(req, dst)
}

func (s Source) do(req *http.Request, dst any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("mem0: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
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

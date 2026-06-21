// Package embed is Baseline's own embedder client (§11.1). Baseline maintains its
// own fact embeddings, fully decoupled from any memory backend's vector store.
// The embedder's output dimension MUST equal the facts.embedding vector(N)
// column width — a mismatch is a startup error, not a silent runtime failure.
//
// PROVIDER SUPPORT: this client speaks the OLLAMA embeddings wire format ONLY —
// POST /api/embeddings, no auth, {"model","prompt"} → {"embedding":[…]}. OpenAI
// (and other providers) are NOT supported: OpenAI uses POST /v1/embeddings,
// bearer auth, an "input" field, a {"data":[{"embedding":[…]}]} envelope, and
// 1536-dim vectors by default. Adding OpenAI (e.g. for the Pi cluster, mirroring
// Mem0's OpenAI fallback) is a clean follow-up — the Embedder interface seam in
// promotions/server already isolates callers from this concrete client — but it
// needs a provider adapter plus a dimension decision (request OpenAI's
// `dimensions: 768` to keep the fixed vector(768) column, or make the column
// width configurable).
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client talks to an Ollama-compatible embeddings endpoint.
type Client struct {
	baseURL string
	model   string
	dims    int
	http    *http.Client
}

// New builds an embedder client. dims is the expected output dimension and is
// enforced on every Embed call (see DimsGuard for the startup check).
func New(baseURL, model string, dims int) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		dims:    dims,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Dims returns the configured embedding dimension.
func (c *Client) Dims() int { return c.dims }

type embedReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embedResp struct {
	Embedding []float32 `json:"embedding"`
}

// Embed returns the embedding for text. It FAILS if the returned vector's length
// does not equal the configured dims — mirroring the chart's dimension footgun
// guard so a misconfigured embedder can never write a wrong-width vector.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	b, err := json.Marshal(embedReq{Model: c.model, Prompt: text})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embeddings", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embed: status %d", resp.StatusCode)
	}
	var out embedResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode: %w", err)
	}
	if len(out.Embedding) != c.dims {
		return nil, fmt.Errorf("embed: dimension mismatch: got %d, want %d (check EMBEDDER_DIMS vs facts.embedding vector(N))",
			len(out.Embedding), c.dims)
	}
	return out.Embedding, nil
}

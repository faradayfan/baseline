// Package mcpbridge exposes Baseline's §9 MCP tools as a thin bridge over the
// REST API. Each tool builds a synthetic HTTP request and dispatches it through
// the SAME http.Handler the network server uses, so authn, RBAC, validation, and
// audit are reused verbatim — there is no business logic here, and the §14
// invariants hold automatically (§9 "thin bridge over the above").
//
// Identity: a bridge is constructed per session with the caller's principal,
// which is injected as X-Baseline-Principal on every synthetic request (matching
// the dev HeaderAuthenticator; production resolves the principal from the MCP
// transport's auth before constructing the bridge).
package mcpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bridge dispatches MCP tool calls into an http.Handler as a fixed principal.
type Bridge struct {
	handler   http.Handler
	principal string
}

// New returns a bridge that calls handler as principal. handler is typically
// server.Handler().
func New(handler http.Handler, principal string) *Bridge {
	return &Bridge{handler: handler, principal: principal}
}

// PrincipalHeader is the request header carrying the caller's identity over the
// HTTP MCP transport. This is the dev/POC identity mechanism (the same header the
// REST HeaderAuthenticator reads); production resolves the principal from OIDC/
// mTLS and this header is ignored.
const PrincipalHeader = "X-Baseline-Principal"

// HTTPHandler returns an http.Handler that serves the MCP tools over the
// streamable-HTTP transport, deriving the principal PER REQUEST from the
// X-Baseline-Principal header. Mount it (e.g. at /mcp) so remote clients — like a
// Claude instance pointed at a hosted Baseline — can connect over the network
// instead of launching a local stdio subprocess.
//
// Identity isolation is the security-relevant property here: each request builds
// a Bridge bound to that request's principal, so two callers with different
// headers get their own entitlements and can never see each other's scope.
//
// The session is Stateless (no server-side session map): each request is
// self-contained, which suits a hosted multi-client service.
func HTTPHandler(handler http.Handler) http.Handler {
	getServer := func(r *http.Request) *mcp.Server {
		principal := r.Header.Get(PrincipalHeader)
		// An empty principal yields a bridge that the REST layer will reject with
		// 401 on every tool call (unauthenticated) — fail closed, no anonymous access.
		return New(handler, principal).Server()
	}
	return mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})
}

// Server builds an *mcp.Server with the five §9 tools registered. Serve it over
// any MCP transport (stdio, streamable HTTP).
func (b *Bridge) Server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "baseline",
		Title:   "Baseline Facts Management",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_context",
		Description: "Return the precedence-resolved facts (and optionally personal memories) the caller is entitled to. Optionally narrow with `tags` (comma-separated; returns facts carrying ANY of them, plus always-on authoritative baselines). Maps to GET /context.",
	}, wrap(b.getContext))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_facts",
		Description: "Search active facts the caller can read. With `q`, results are ranked by semantic relevance to the query (meaning, not exact words). Optionally narrow with `tags` (comma-separated, ANY-match; authoritative facts always included). Maps to GET /facts.",
	}, wrap(b.searchFacts))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "propose_fact",
		Description: "Propose a memory/statement for promotion into a namespace. Optional `tags` (string array) label the fact for read-path filtering. Maps to POST /promotions.",
	}, wrap(b.proposeFact))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_my_promotions",
		Description: "List the caller's own promotion requests. Maps to GET /promotions?proposer=me.",
	}, wrap(b.listMyPromotions))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "review_promotion",
		Description: "Approve, reject, or request changes on a promotion. Maps to the promotion review actions.",
	}, wrap(b.reviewPromotion))

	return s
}

// wrap adapts a typed-input bridge handler to the SDK's ToolHandlerFor with an
// `any` output type. Using `any` (not a concrete struct) is deliberate: it tells
// the SDK to infer NO output schema, so it skips output validation for the opaque
// REST body whose shape varies per endpoint. The handler still returns the parsed
// body as the output value, which the SDK places in StructuredContent — so the
// result carries both human-readable text and a structured payload.
func wrap[In any](fn func(context.Context, In) (*mcp.CallToolResult, any, error)) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		return fn(ctx, in)
	}
}

// --- tool input/output types ---

type getContextIn struct {
	ActorID         string `json:"actor_id,omitempty"`
	Namespaces      string `json:"namespaces,omitempty"` // comma-separated ids
	IncludeMemories bool   `json:"include_memories,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Tags            string `json:"tags,omitempty"` // comma-separated; ANY-match (authoritative always included)
}

type searchFactsIn struct {
	Query     string `json:"q,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Tags      string `json:"tags,omitempty"` // comma-separated; ANY-match (authoritative always included)
	Limit     int    `json:"limit,omitempty"`
}

type proposeFactIn struct {
	TargetNamespace    string         `json:"target_namespace"`
	ProposedStatement  string         `json:"proposed_statement"`
	Subject            map[string]any `json:"subject"`
	CandidateMemoryIDs []string       `json:"candidate_memory_ids,omitempty"`
	Tags               []string       `json:"tags,omitempty"` // labels for read-path filtering
}

type listMyPromotionsIn struct {
	State string `json:"state,omitempty"`
}

type reviewPromotionIn struct {
	PromotionID        string `json:"promotion_id"`
	Action             string `json:"action"` // approve | reject | request-changes
	Comment            string `json:"comment,omitempty"`
	SuggestedStatement string `json:"suggested_statement,omitempty"`
}

// --- tool handlers (each is a thin map onto one REST call) ---

func (b *Bridge) getContext(ctx context.Context, in getContextIn) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	if in.ActorID != "" {
		q.Set("actor_id", in.ActorID)
	}
	if in.Namespaces != "" {
		q.Set("namespaces", in.Namespaces)
	}
	if in.IncludeMemories {
		q.Set("include_memories", "true")
	}
	if in.Tags != "" {
		q.Set("tags", in.Tags)
	}
	if in.Limit > 0 {
		q.Set("limit", fmt.Sprint(in.Limit))
	}
	return b.call(ctx, http.MethodGet, "/v1/context?"+q.Encode(), nil)
}

func (b *Bridge) searchFacts(ctx context.Context, in searchFactsIn) (*mcp.CallToolResult, any, error) {
	q := url.Values{}
	for k, v := range map[string]string{"q": in.Query, "namespace": in.Namespace, "status": in.Status, "tag": in.Tag, "tags": in.Tags} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if in.Limit > 0 {
		q.Set("limit", fmt.Sprint(in.Limit))
	}
	return b.call(ctx, http.MethodGet, "/v1/facts?"+q.Encode(), nil)
}

func (b *Bridge) proposeFact(ctx context.Context, in proposeFactIn) (*mcp.CallToolResult, any, error) {
	body := map[string]any{
		"target_namespace":     in.TargetNamespace,
		"proposed_statement":   in.ProposedStatement,
		"subject":              in.Subject,
		"candidate_memory_ids": in.CandidateMemoryIDs,
		"tags":                 in.Tags,
	}
	return b.call(ctx, http.MethodPost, "/v1/promotions", body)
}

func (b *Bridge) listMyPromotions(ctx context.Context, in listMyPromotionsIn) (*mcp.CallToolResult, any, error) {
	q := url.Values{"proposer": {"me"}}
	if in.State != "" {
		q.Set("state", in.State)
	}
	return b.call(ctx, http.MethodGet, "/v1/promotions?"+q.Encode(), nil)
}

func (b *Bridge) reviewPromotion(ctx context.Context, in reviewPromotionIn) (*mcp.CallToolResult, any, error) {
	switch in.Action {
	case "approve", "reject", "request-changes":
	default:
		return errorResult("action must be approve|reject|request-changes"), nil, nil
	}
	body := map[string]any{"comment": in.Comment}
	if in.SuggestedStatement != "" {
		body["suggested_statement"] = in.SuggestedStatement
	}
	return b.call(ctx, http.MethodPost, "/v1/promotions/"+url.PathEscape(in.PromotionID)+"/"+in.Action, body)
}

// call dispatches a synthetic request through the REST handler as the bridge's
// principal and returns the response BOTH as human-readable JSON text content and
// as a structured output value (placed in StructuredContent by the SDK). The REST
// body — whose shape varies per endpoint — is parsed and wrapped as
// {"result": <body>} so consumers get a stable, structured envelope. Non-2xx
// responses become tool errors carrying the REST error envelope.
func (b *Bridge) call(ctx context.Context, method, path string, body any) (*mcp.CallToolResult, any, error) {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return errorResult("encode request: " + err.Error()), nil, nil
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader).WithContext(ctx)
	req.Header.Set("X-Baseline-Principal", b.principal)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	b.handler.ServeHTTP(rec, req)

	respBody := rec.Body.String()
	if rec.Code < 200 || rec.Code >= 300 {
		return errorResult(fmt.Sprintf("baseline %s %s -> %d: %s", method, path, rec.Code, respBody)), nil, nil
	}

	// Parse the body so it surfaces as structured content. If it isn't JSON for
	// some reason, fall back to the raw string under the same envelope.
	var parsed any
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		parsed = respBody
	}
	out := map[string]any{"result": parsed}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: respBody}},
	}, out, nil
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

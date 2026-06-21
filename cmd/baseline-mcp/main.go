// Command baseline-mcp is a tiny client for Baseline's MCP-over-HTTP endpoint.
//
// It exists so nobody has to hand-roll a curl + SSE-parse one-liner to drive the
// MCP tools (get_context, search_facts, list_namespaces, propose_fact,
// submit_promotion, list_my_promotions, review_promotion). The /mcp transport
// answers a bare tools/call (no initialize handshake) and replies as Server-Sent
// Events: an `event: message` line then a `data: {json-rpc}` line, where the
// result's text content is itself a JSON-encoded string. This tool unwraps both
// layers and prints the inner payload as indented JSON.
//
// Usage:
//
//	baseline-mcp <tool> [--principal you] [--url http://localhost:8080]
//	             [--arg key=value ...] [--json '{"k":"v"}'] [--raw]
//
// --arg values are parsed smartly: JSON literals (objects, arrays, numbers,
// true/false/null) are passed through as-is; everything else is a string. So:
//
//	baseline-mcp list_namespaces
//	baseline-mcp search_facts --arg q="vulnerability scanning"
//	baseline-mcp propose_fact \
//	  --arg target_namespace=<id> \
//	  --arg proposed_statement="All projects must run grype scanning." \
//	  --arg subject='{"type":"security.scan","scope":"global"}' \
//	  --arg tags='["security","tier:always"]'
//	baseline-mcp submit_promotion --arg promotion_id=<id>
//	baseline-mcp review_promotion --arg promotion_id=<id> --arg action=approve
//
// Env fallbacks (so flags can be omitted): BASELINE_MCP_URL / BASELINE_URL for
// --url, BASELINE_PRINCIPAL for --principal, BASELINE_API_TOKEN for the bearer.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// version is stamped at release build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println("baseline-mcp " + version)
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "baseline-mcp: "+err.Error())
		os.Exit(1)
	}
}

// argList collects repeated --arg key=value flags.
type argList []string

func (a *argList) String() string { return strings.Join(*a, ",") }
func (a *argList) Set(v string) error {
	*a = append(*a, v)
	return nil
}

// valueFlags are the flags that consume the following argv element as their
// value (vs. --flag=value or bool flags). splitToolAndFlags needs this so it does
// not mistake a flag's value (e.g. the `you` in `--principal you`) for the tool.
var valueFlags = map[string]bool{
	"--url": true, "--principal": true, "--token": true,
	"--json": true, "--arg": true,
	"-url": true, "-principal": true, "-token": true, "-json": true, "-arg": true,
}

// splitToolAndFlags separates the positional tool name from the flag arguments,
// so the natural form `baseline-mcp <tool> --flag…` works. Go's flag package
// stops parsing at the first non-flag token, which would otherwise silently drop
// every flag after the tool name. We pull the FIRST bare (non-flag, non-flag-
// value) token out as the tool and hand the rest to flag.Parse. Returns an empty
// tool when there is none (e.g. `--list-tools`).
func splitToolAndFlags(argv []string) (tool string, rest []string) {
	expectValue := false
	for _, a := range argv {
		switch {
		case expectValue:
			// This token is the value for the preceding value-flag — keep it.
			expectValue = false
			rest = append(rest, a)
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
			// `--flag value` form consumes the next token; `--flag=value` doesn't.
			if valueFlags[a] && !strings.Contains(a, "=") {
				expectValue = true
			}
		case tool == "":
			tool = a // the first bare token is the tool name
		default:
			rest = append(rest, a) // extra positionals (none expected) pass through
		}
	}
	return tool, rest
}

func run(argv []string) error {
	// Pull the positional tool name out before flag parsing (see
	// splitToolAndFlags) so the tool name can come first while flags still parse.
	tool, rest := splitToolAndFlags(argv)

	fs := flag.NewFlagSet("baseline-mcp", flag.ContinueOnError)
	var (
		url       = fs.String("url", envOr("BASELINE_MCP_URL", envOr("BASELINE_URL", "http://localhost:8080")), "Baseline base URL (the /mcp path is appended)")
		principal = fs.String("principal", os.Getenv("BASELINE_PRINCIPAL"), "X-Baseline-Principal identity to act as")
		token     = fs.String("token", os.Getenv("BASELINE_API_TOKEN"), "bearer token, if the deployment requires auth")
		jsonArgs  = fs.String("json", "", "full tool arguments as a JSON object (overrides --arg)")
		raw       = fs.Bool("raw", false, "print the raw inner text instead of re-indented JSON")
		list      = fs.Bool("list-tools", false, "list available tools instead of calling one")
	)
	var args argList
	fs.Var(&args, "arg", "a key=value tool argument; repeatable. Value may be a JSON literal.")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: baseline-mcp <tool> [--principal you] [--arg key=value ...] [--json '{...}']")
		fmt.Fprintln(os.Stderr, "       baseline-mcp --list-tools")
		fs.PrintDefaults()
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}

	if *list {
		return rpc(*url, *principal, *token, "tools/list", map[string]any{}, *raw)
	}

	if tool == "" {
		fs.Usage()
		return errors.New("a tool name is required")
	}

	arguments, err := buildArgs(*jsonArgs, args)
	if err != nil {
		return err
	}
	return rpc(*url, *principal, *token, "tools/call",
		map[string]any{"name": tool, "arguments": arguments}, *raw)
}

// buildArgs assembles the tool arguments from --json (if set) or the --arg pairs.
func buildArgs(jsonArgs string, pairs argList) (map[string]any, error) {
	if jsonArgs != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(jsonArgs), &m); err != nil {
			return nil, fmt.Errorf("--json is not a valid JSON object: %w", err)
		}
		return m, nil
	}
	m := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("--arg %q is not key=value", p)
		}
		m[k] = smartValue(v)
	}
	return m, nil
}

// smartValue passes a value through as a parsed JSON literal when it is one
// (object, array, number, bool, null); otherwise it is a plain string. This lets
// --arg subject='{"type":"x"}' and --arg tags='["a","b"]' work without quoting
// gymnastics, while --arg action=approve stays a string.
func smartValue(v string) any {
	t := strings.TrimSpace(v)
	if t == "" {
		return v
	}
	switch t[0] {
	case '{', '[', 't', 'f', 'n', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var parsed any
		if err := json.Unmarshal([]byte(t), &parsed); err == nil {
			return parsed
		}
	}
	return v
}

// rpc performs one JSON-RPC call over the MCP HTTP transport and prints the
// unwrapped result.
func rpc(baseURL, principal, token, method string, params map[string]any, raw bool) error {
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	endpoint := strings.TrimRight(baseURL, "/") + "/mcp"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// MCP-over-HTTP replies as SSE; this Accept is required by the transport.
	req.Header.Set("Accept", "application/json, text/event-stream")
	if principal != "" {
		req.Header.Set("X-Baseline-Principal", principal)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("401 unauthorized — set --principal (or BASELINE_PRINCIPAL)")
	}

	payload, err := parseSSE(body)
	if err != nil {
		return fmt.Errorf("%w\nraw response:\n%s", err, string(body))
	}
	if rpcErr, ok := payload["error"]; ok {
		return fmt.Errorf("rpc error: %s", mustJSON(rpcErr))
	}
	result, _ := payload["result"].(map[string]any)
	return printResult(result, raw)
}

// parseSSE extracts the JSON-RPC object from an SSE body (the `data:` line). The
// transport may stream multiple events; the JSON-RPC response is the one carrying
// "jsonrpc". strict=false equivalent: encoding/json already tolerates the content.
func parseSSE(body []byte) (map[string]any, error) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			continue // not the JSON-RPC frame; keep scanning
		}
		if _, ok := m["jsonrpc"]; ok {
			return m, nil
		}
	}
	return nil, errors.New("no JSON-RPC data frame found in MCP response")
}

// printResult unwraps the tool result. A tools/call result wraps its payload in
// content[].text as a JSON-encoded string; tools/list returns tools directly.
// isError surfaces tool-level failures distinctly from transport errors.
func printResult(result map[string]any, raw bool) error {
	if result == nil {
		return errors.New("empty result")
	}
	if isErr, _ := result["isError"].(bool); isErr {
		return fmt.Errorf("tool reported an error:\n%s", mustJSON(result["content"]))
	}
	// tools/list: print the tool names + descriptions.
	if tools, ok := result["tools"].([]any); ok {
		for _, t := range tools {
			if tm, ok := t.(map[string]any); ok {
				fmt.Printf("%-22s %s\n", tm["name"], tm["description"])
			}
		}
		return nil
	}
	// tools/call: unwrap content[0].text (a JSON-encoded string).
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		fmt.Println(mustJSON(result))
		return nil
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if raw {
		fmt.Println(text)
		return nil
	}
	var inner any
	if err := json.Unmarshal([]byte(text), &inner); err != nil {
		// Not JSON (e.g. a plain status message) — print verbatim.
		fmt.Println(text)
		return nil
	}
	fmt.Println(mustJSON(inner))
	return nil
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

package main

import "testing"

// TestSmartValue covers the JSON-literal-vs-string heuristic that lets
// --arg subject='{...}' and --arg tags='[...]' work while keeping
// --arg action=approve a plain string.
func TestSmartValue(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"approve", "approve"},                         // plain string
		{"50ee83e9-uuid", "50ee83e9-uuid"},             // not a JSON literal
		{`{"type":"x"}`, map[string]any{"type": "x"}},  // object
		{`["a","b"]`, []any{"a", "b"}},                 // array
		{"true", true},                                 // bool
		{"42", float64(42)},                            // number
		{"", ""},                                       // empty stays empty string
		{"3-tier", "3-tier"},                           // starts digit but not valid JSON → string
	}
	for _, c := range cases {
		got := smartValue(c.in)
		if !equal(got, c.want) {
			t.Errorf("smartValue(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	// --json wins and parses an object.
	m, err := buildArgs(`{"a":1,"b":"x"}`, nil)
	if err != nil || m["b"] != "x" {
		t.Fatalf("buildArgs(--json) = %v, %v", m, err)
	}
	// --arg pairs, with a JSON-literal value.
	m, err = buildArgs("", argList{"action=approve", `subject={"type":"t"}`})
	if err != nil {
		t.Fatal(err)
	}
	if m["action"] != "approve" {
		t.Errorf("action = %v, want approve", m["action"])
	}
	if subj, ok := m["subject"].(map[string]any); !ok || subj["type"] != "t" {
		t.Errorf("subject not parsed as object: %#v", m["subject"])
	}
	// malformed --arg (no =) is an error.
	if _, err := buildArgs("", argList{"oops"}); err == nil {
		t.Error("expected error for --arg without '='")
	}
}

// TestParseSSE asserts the SSE 'data:' frame is extracted — the exact step that
// broke naive shell parsing.
func TestParseSSE(t *testing.T) {
	body := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}\n")
	m, err := parseSSE(body)
	if err != nil {
		t.Fatal(err)
	}
	if m["jsonrpc"] != "2.0" {
		t.Errorf("did not extract the JSON-RPC frame: %#v", m)
	}
	// no data frame → error
	if _, err := parseSSE([]byte("event: ping\n")); err == nil {
		t.Error("expected error when no data frame present")
	}
}

func equal(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !equal(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !equal(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

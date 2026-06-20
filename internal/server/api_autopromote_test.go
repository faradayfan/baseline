package server_test

import (
	"net/http"
	"testing"
)

// TestAPI_PolicyValidation_RejectsBadEngine asserts §14.15 at the API: a
// namespace policy naming an unknown engine, or rules the pinned engine can't
// validate, is rejected at write time (400). A valid policy is accepted.
func TestAPI_PolicyValidation_RejectsBadEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := orgWithApprovals(t, pool, 1)
	grant(t, pool, "admin1", org, "namespace_admin")

	cases := []struct {
		name   string
		policy map[string]any
		want   int
	}{
		{
			"unknown engine id",
			map[string]any{"required_approvals": 1, "auto_promote": map[string]any{"engine": "cel/v1"}},
			http.StatusBadRequest,
		},
		{
			"invalid rules for simple/v1 (non-whitelisted field)",
			map[string]any{"required_approvals": 1, "auto_promote": map[string]any{
				"engine": "simple/v1",
				"rules":  map[string]any{"rules": []any{map[string]any{"all": []any{map[string]any{"field": "statement", "op": "eq", "value": "x"}}}}},
			}},
			http.StatusBadRequest,
		},
		{
			"valid simple/v1 policy",
			map[string]any{"required_approvals": 1, "auto_promote": map[string]any{
				"engine": "simple/v1",
				"rules":  map[string]any{"rules": []any{map[string]any{"all": []any{map[string]any{"field": "actor.type", "op": "eq", "value": "human"}}}}},
			}},
			http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := api.Do(t, "PATCH", "/v1/namespaces/"+org.String(), tc.policy, hdr("admin1"))
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

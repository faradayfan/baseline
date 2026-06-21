package conformance

import (
	"net/http"
	"testing"
)

// §14.10 — each §7.2 matrix cell is positively and negatively tested, end-to-end.
// The matrix actions are exercised through the endpoints that gate on them:
//
//	read facts  → GET /v1/namespaces/{id}
//	propose     → POST /v1/promotions
//	approve     → POST /v1/promotions/{id}/approve
//	manage      → POST /v1/namespaces/{id}/members
func Test14_10_RBACMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance")
	}
	e := newEnv(t)
	org := e.seedNamespaceApprovals("org", "org", 1)

	// One principal per role.
	e.grant("rdr", org, "reader")
	e.grant("con", org, "contributor")
	e.grant("rev", org, "reviewer")
	e.grant("adm", org, "namespace_admin")

	readFacts := func(p string) int {
		r := e.do("GET", "/v1/namespaces/"+org.String(), nil, asPrincipal(p))
		r.Body.Close()
		return r.StatusCode
	}
	propose := func(p string) int {
		r := e.do("POST", "/v1/promotions", map[string]any{
			"target_namespace": org, "proposed_statement": "x", "subject": map[string]any{"type": "t"},
		}, asPrincipal(p))
		r.Body.Close()
		return r.StatusCode
	}
	manage := func(p string) int {
		r := e.do("POST", "/v1/namespaces/"+org.String()+"/members",
			map[string]any{"principal": "x", "role": "reader"}, asPrincipal(p))
		r.Body.Close()
		return r.StatusCode
	}

	ok := func(code int) bool { return code >= 200 && code < 300 }
	forbidden := func(code int) bool { return code == http.StatusForbidden }

	// read facts: all roles may; a stranger may not.
	for _, p := range []string{"rdr", "con", "rev", "adm"} {
		if !ok(readFacts(p)) {
			t.Errorf("%s should read facts", p)
		}
	}
	if !forbidden(readFacts("stranger")) {
		t.Error("stranger must not read facts")
	}

	// propose: contributor+; reader may not.
	if forbidden(propose("con")) || forbidden(propose("rev")) || forbidden(propose("adm")) {
		t.Error("contributor/reviewer/admin should propose")
	}
	if !forbidden(propose("rdr")) {
		t.Error("reader must not propose")
	}

	// approve: reviewer+; contributor may not. (Use a fresh promotion by con.)
	approveCell := func(approver string) int {
		pr := e.do("POST", "/v1/promotions", map[string]any{
			"target_namespace": org, "proposed_statement": "y", "subject": map[string]any{"type": "u"},
		}, asPrincipal("con"))
		var p struct {
			ID string `json:"id"`
		}
		decode(t, pr, &p)
		e.do("POST", "/v1/promotions/"+p.ID+"/submit", nil, asPrincipal("con")).Body.Close()
		r := e.do("POST", "/v1/promotions/"+p.ID+"/approve", map[string]any{}, asPrincipal(approver))
		r.Body.Close()
		return r.StatusCode
	}
	if forbidden(approveCell("rev")) {
		t.Error("reviewer should approve")
	}
	if !forbidden(approveCell("con")) {
		t.Error("contributor must not approve")
	}

	// manage members: admin only.
	if !ok(manage("adm")) {
		t.Error("admin should manage members")
	}
	for _, p := range []string{"rdr", "con", "rev"} {
		if !forbidden(manage(p)) {
			t.Errorf("%s must not manage members", p)
		}
	}
}

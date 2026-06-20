package server_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/storetest"
)

// orgWithApprovals seeds an org namespace with the given required_approvals.
func orgWithApprovals(t *testing.T, pool *pgxpool.Pool, n int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind, policy) VALUES ('org','org',$1) RETURNING id`,
		[]byte(`{"required_approvals":`+strconv.Itoa(n)+`}`)).Scan(&id)
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return id
}

// proposeViaAPI proposes and returns the promotion id + fact id.
func proposeViaAPI(t *testing.T, api *storetest.API, ns uuid.UUID, proposer string) (string, string) {
	t.Helper()
	body := map[string]any{
		"target_namespace":   ns,
		"proposed_statement": "all deploys go through CI",
		"subject":            map[string]any{"type": "deploy.policy", "scope": "global"},
	}
	resp := api.Do(t, "POST", "/v1/promotions", body, hdr(proposer))
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("propose status = %d, want 201", resp.StatusCode)
	}
	var p struct {
		ID     string `json:"id"`
		FactID string `json:"fact_id"`
	}
	storetest.DecodeJSON(t, resp, &p)
	return p.ID, p.FactID
}

func post(t *testing.T, api *storetest.API, path, principal string) *http.Response {
	t.Helper()
	return api.Do(t, "POST", path, map[string]any{"comment": "ok"}, hdr(principal))
}

// TestAPI_PromotionHappyPath_ApprovalActivates exercises §14.1 end-to-end:
// propose → submit → two distinct approvals → fact active.
func TestAPI_PromotionHappyPath_ApprovalActivates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := orgWithApprovals(t, pool, 2)
	grant(t, pool, "alice", org, "contributor")
	grant(t, pool, "bob", org, "reviewer")
	grant(t, pool, "carol", org, "reviewer")

	promoID, factID := proposeViaAPI(t, api, org, "alice")
	post(t, api, "/v1/promotions/"+promoID+"/submit", "alice").Body.Close()

	// One approval: fact not yet active.
	post(t, api, "/v1/promotions/"+promoID+"/approve", "bob").Body.Close()
	resp := api.Do(t, "GET", "/v1/facts/"+factID, nil, hdr("bob"))
	var f struct {
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	storetest.DecodeJSON(t, resp, &f)
	if f.Status == "active" {
		t.Fatal("fact active after only 1 of 2 approvals")
	}

	// Second distinct approval: fact active.
	post(t, api, "/v1/promotions/"+promoID+"/approve", "carol").Body.Close()
	resp = api.Do(t, "GET", "/v1/facts/"+factID, nil, hdr("bob"))
	storetest.DecodeJSON(t, resp, &f)
	if f.Status != "active" {
		t.Errorf("fact status = %s, want active", f.Status)
	}
}

// TestAPI_SeparationOfDuties asserts §14.6 over HTTP: proposer approving their
// own promotion is forbidden (403).
func TestAPI_SeparationOfDuties(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := orgWithApprovals(t, pool, 1)
	// alice is both contributor and reviewer — she still can't approve her own.
	grant(t, pool, "alice", org, "contributor")
	grant(t, pool, "alice", org, "reviewer")

	promoID, _ := proposeViaAPI(t, api, org, "alice")
	post(t, api, "/v1/promotions/"+promoID+"/submit", "alice").Body.Close()

	resp := post(t, api, "/v1/promotions/"+promoID+"/approve", "alice")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("self-approve status = %d, want 403", resp.StatusCode)
	}
}

// TestAPI_ProposeRequiresContributor asserts a reader cannot propose (403, §14.10
// for the propose cell, exercised through the promotions endpoint).
func TestAPI_ProposeRequiresContributor(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := orgWithApprovals(t, pool, 1)
	grant(t, pool, "ron", org, "reader")

	body := map[string]any{
		"target_namespace":   org,
		"proposed_statement": "x",
		"subject":            map[string]any{"type": "t"},
	}
	resp := api.Do(t, "POST", "/v1/promotions", body, hdr("ron"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("reader propose = %d, want 403", resp.StatusCode)
	}
}

// TestAPI_RevokeOptimisticConcurrency asserts §14.8: a PATCH revoke with a stale
// If-Match version returns 409 and does not revoke.
func TestAPI_RevokeOptimisticConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := orgWithApprovals(t, pool, 1)
	grant(t, pool, "alice", org, "contributor")
	grant(t, pool, "bob", org, "reviewer")

	promoID, factID := proposeViaAPI(t, api, org, "alice")
	post(t, api, "/v1/promotions/"+promoID+"/submit", "alice").Body.Close()
	post(t, api, "/v1/promotions/"+promoID+"/approve", "bob").Body.Close()

	// Read current version.
	resp := api.Do(t, "GET", "/v1/facts/"+factID, nil, hdr("bob"))
	var f struct {
		Version int `json:"version"`
	}
	storetest.DecodeJSON(t, resp, &f)

	// Stale version → 409.
	stale := map[string]string{"X-Baseline-Principal": "bob", "If-Match": strconv.Itoa(f.Version - 1)}
	resp = api.Do(t, "PATCH", "/v1/facts/"+factID, nil, stale)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("stale revoke = %d, want 409", resp.StatusCode)
	}

	// Correct version → 200.
	good := map[string]string{"X-Baseline-Principal": "bob", "If-Match": strconv.Itoa(f.Version)}
	resp = api.Do(t, "PATCH", "/v1/facts/"+factID, nil, good)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct revoke = %d, want 200", resp.StatusCode)
	}
}

// TestAPI_FactHistory asserts the audit trail is queryable and records the
// lifecycle transitions (§14.5 surfaced via the history endpoint).
func TestAPI_FactHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	api, pool := newAPI(t)
	org := orgWithApprovals(t, pool, 1)
	grant(t, pool, "alice", org, "contributor")
	grant(t, pool, "bob", org, "reviewer")

	promoID, factID := proposeViaAPI(t, api, org, "alice")
	post(t, api, "/v1/promotions/"+promoID+"/submit", "alice").Body.Close()
	post(t, api, "/v1/promotions/"+promoID+"/approve", "bob").Body.Close()

	resp := api.Do(t, "GET", "/v1/facts/"+factID+"/history", nil, hdr("bob"))
	var hist []map[string]any
	storetest.DecodeJSON(t, resp, &hist)
	if len(hist) == 0 {
		t.Error("expected audit history for the fact")
	}
}

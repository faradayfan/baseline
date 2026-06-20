package promotions_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/autopromote"
	"github.com/faradayfan/baseline/internal/autopromote/simple"
	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/promotions"
	"github.com/faradayfan/baseline/internal/storetest"
)

// setupAuto seeds a team namespace whose policy auto-promotes facts derived from
// a merged PR by a human/team-agent, with a registry holding simple/v1.
func setupAuto(t *testing.T) (*promotions.Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	h := storetest.Shared(t)
	pool := h.FreshDB(t)
	nsRepo := namespaces.NewRepo(pool)

	rules := []byte(`{"rules":[{"all":[
		{"field":"provenance.origin_type","op":"eq","value":"merged_pr"},
		{"field":"actor.type","op":"in","value":["human","team-agent"]}
	]}]}`)
	ns, err := nsRepo.Create(context.Background(), namespaces.Namespace{
		Name: "team:eng", Kind: namespaces.KindTeam,
		Policy: namespaces.Policy{
			RequiredApprovals: 1,
			AutoPromote:       &namespaces.AutoPromote{Engine: simple.ID, Rules: rules},
		},
	})
	if err != nil {
		t.Fatalf("seed ns: %v", err)
	}
	reg := autopromote.NewRegistry(simple.New())
	return promotions.NewService(pool, nsRepo, reg), pool, ns.ID
}

// TestAutoPromote_MatchActivatesWithAttribution asserts §14.12: a matching
// candidate is auto-promoted to active, tagged auto:true, with an audit event
// attributed to engine:simple/v1.
func TestAutoPromote_MatchActivatesWithAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, ns := setupAuto(t)
	ctx := context.Background()

	p, err := svc.Propose(ctx, promotions.ProposeInput{
		TargetNamespaceID: ns,
		ProposedStatement: "main builds with bazel",
		Subject:           facts.Subject{Type: "build.command", Scope: "main"},
		Proposer:          "alice",
		OriginType:        "merged_pr",
		ActorType:         "human",
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if p.State != promotions.StateApproved {
		t.Errorf("promotion state = %s, want approved (auto)", p.State)
	}

	f, err := facts.Get(ctx, pool, p.FactID)
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != facts.StatusActive {
		t.Errorf("fact status = %s, want active", f.Status)
	}
	if !hasTag(f.Tags, "auto:true") {
		t.Errorf("fact tags = %v, want auto:true", f.Tags)
	}

	// Audit attribution: engine:simple/v1.
	var principal string
	err = pool.QueryRow(ctx,
		`SELECT principal FROM audit_events WHERE subject_id = $1 AND action = 'fact.auto_promoted'`, p.FactID).Scan(&principal)
	if err != nil {
		t.Fatalf("auto_promoted audit event missing: %v", err)
	}
	if principal != "engine:simple/v1" {
		t.Errorf("audit principal = %q, want engine:simple/v1", principal)
	}
}

// TestAutoPromote_NonMatchFallsThrough asserts §14.11 fail-closed: a candidate
// that doesn't match stays pending for human review (not auto-promoted).
func TestAutoPromote_NonMatchFallsThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, ns := setupAuto(t)
	ctx := context.Background()

	p, err := svc.Propose(ctx, promotions.ProposeInput{
		TargetNamespaceID: ns,
		ProposedStatement: "manual note",
		Subject:           facts.Subject{Type: "note"},
		Proposer:          "alice",
		OriginType:        "manual", // does not match merged_pr
		ActorType:         "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != promotions.StatePending {
		t.Errorf("state = %s, want pending (human review)", p.State)
	}
	f, _ := facts.Get(ctx, pool, p.FactID)
	if f.Status != facts.StatusProposed {
		t.Errorf("fact status = %s, want proposed", f.Status)
	}
}

// TestAutoPromote_NoEngineConfigured: a namespace without auto_promote never
// auto-promotes even with a matching-looking candidate.
func TestAutoPromote_NoEngineConfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, _, org := setup(t) // org has no auto_promote
	p, err := svc.Propose(context.Background(), promotions.ProposeInput{
		TargetNamespaceID: org, ProposedStatement: "x", Subject: facts.Subject{Type: "t"},
		Proposer: "alice", OriginType: "merged_pr", ActorType: "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != promotions.StatePending {
		t.Errorf("state = %s, want pending (no engine)", p.State)
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

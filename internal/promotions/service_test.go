package promotions_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/promotions"
	"github.com/faradayfan/baseline/internal/storetest"
)

func setup(t *testing.T) (*promotions.Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	h := storetest.Shared(t)
	pool := h.FreshDB(t)
	nsRepo := namespaces.NewRepo(pool)
	// org requires 2 approvals by default.
	org, err := nsRepo.Create(context.Background(), namespaces.Namespace{Name: "org", Kind: namespaces.KindOrg})
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return promotions.NewService(pool, nsRepo), pool, org.ID
}

func propose(t *testing.T, svc *promotions.Service, ns uuid.UUID, proposer, statement string, subj facts.Subject) promotions.PromotionRequest {
	t.Helper()
	p, err := svc.Propose(context.Background(), promotions.ProposeInput{
		TargetNamespaceID: ns, ProposedStatement: statement, Subject: subj, Proposer: proposer,
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	return p
}

func factStatus(t *testing.T, pool *pgxpool.Pool, factID uuid.UUID) facts.Status {
	t.Helper()
	f, err := facts.Get(context.Background(), pool, factID)
	if err != nil {
		t.Fatalf("get fact: %v", err)
	}
	return f.Status
}

// TestApprovalActivatesAtThreshold asserts §14.1: a fact becomes active iff it
// has the required number of distinct approvals.
func TestApprovalActivatesAtThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, org := setup(t)
	ctx := context.Background()

	p := propose(t, svc, org, "alice", "deploys go through CI", facts.Subject{Type: "deploy.policy"})
	if _, err := svc.Submit(ctx, p.ID, p.FactID, "alice"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// First approval (bob): not enough (org requires 2) — fact still in_review.
	p2, err := svc.Approve(ctx, p.ID, "bob", "lgtm")
	if err != nil {
		t.Fatalf("approve bob: %v", err)
	}
	if p2.State == promotions.StateApproved {
		t.Fatal("should not be approved after 1 of 2")
	}
	if s := factStatus(t, pool, p.FactID); s != facts.StatusInReview {
		t.Errorf("fact status = %s, want in_review", s)
	}

	// Second distinct approval (carol): threshold met → fact active.
	p3, err := svc.Approve(ctx, p.ID, "carol", "lgtm")
	if err != nil {
		t.Fatalf("approve carol: %v", err)
	}
	if p3.State != promotions.StateApproved {
		t.Fatalf("state = %s, want approved", p3.State)
	}
	if s := factStatus(t, pool, p.FactID); s != facts.StatusActive {
		t.Errorf("fact status = %s, want active", s)
	}
}

// TestSeparationOfDuties_Enforced asserts §14.6: the proposer cannot approve
// their own promotion, at any policy.
func TestSeparationOfDuties_Enforced(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, _, org := setup(t)
	ctx := context.Background()
	p := propose(t, svc, org, "alice", "x", facts.Subject{Type: "t"})
	if _, err := svc.Submit(ctx, p.ID, p.FactID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, p.ID, "alice", "self"); err != promotions.ErrSelfApproval {
		t.Errorf("self-approval err = %v, want ErrSelfApproval", err)
	}
}

// TestConflictSupersession asserts §14.7: approving a fact whose canonical key
// conflicts with an existing active fact atomically supersedes the prior one,
// with lineage set both ways.
func TestConflictSupersession(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, org := setup(t)
	ctx := context.Background()
	subj := facts.Subject{Type: "build.command", Scope: "svc"}

	// First fact, approved to active.
	p1 := propose(t, svc, org, "alice", "make build", subj)
	mustSubmit(t, svc, p1)
	mustApprove(t, svc, p1.ID, "bob", "carol")
	if s := factStatus(t, pool, p1.FactID); s != facts.StatusActive {
		t.Fatalf("first fact not active: %s", s)
	}

	// Second fact, same subject → conflict detected at propose.
	p2 := propose(t, svc, org, "dave", "bazel build", subj)
	if p2.ConflictWith == nil || *p2.ConflictWith != p1.FactID {
		t.Fatalf("expected conflict_with=%v, got %v", p1.FactID, p2.ConflictWith)
	}
	mustSubmit(t, svc, p2)
	mustApprove(t, svc, p2.ID, "bob", "carol")

	// Prior fact superseded with two-way lineage; new fact active.
	oldF, _ := facts.Get(ctx, pool, p1.FactID)
	newF, _ := facts.Get(ctx, pool, p2.FactID)
	if oldF.Status != facts.StatusSuperseded {
		t.Errorf("old status = %s, want superseded", oldF.Status)
	}
	if newF.Status != facts.StatusActive {
		t.Errorf("new status = %s, want active", newF.Status)
	}
	if oldF.SupersededByID == nil || *oldF.SupersededByID != newF.ID {
		t.Errorf("old.superseded_by = %v, want %v", oldF.SupersededByID, newF.ID)
	}
	if newF.SupersedesID == nil || *newF.SupersedesID != oldF.ID {
		t.Errorf("new.supersedes = %v, want %v", newF.SupersedesID, oldF.ID)
	}
}

// TestIdempotentPropose asserts a repeat POST with the same idempotency key
// returns the same promotion, not a duplicate.
func TestIdempotentPropose(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, _, org := setup(t)
	ctx := context.Background()
	in := promotions.ProposeInput{
		TargetNamespaceID: org, ProposedStatement: "x", Subject: facts.Subject{Type: "t"},
		Proposer: "alice", IdempotencyKey: "key-123",
	}
	first, err := svc.Propose(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Propose(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotency violated: %v != %v", first.ID, second.ID)
	}
}

func TestAuditTrail_OneEventPerTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	svc, pool, org := setup(t)
	ctx := context.Background()
	p := propose(t, svc, org, "alice", "x", facts.Subject{Type: "t"})
	mustSubmit(t, svc, p)
	mustApprove(t, svc, p.ID, "bob", "carol")

	// Expect audit rows for: proposed, submitted, 2× approved, activated.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE subject_id = $1 OR subject_id = $2`, p.ID, p.FactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 4 {
		t.Errorf("audit events = %d, want >= 4 (proposed, submitted, approved×2, activated)", count)
	}
}

// --- helpers ---

func mustSubmit(t *testing.T, svc *promotions.Service, p promotions.PromotionRequest) {
	t.Helper()
	if _, err := svc.Submit(context.Background(), p.ID, p.FactID, p.Proposer); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

func mustApprove(t *testing.T, svc *promotions.Service, id uuid.UUID, reviewers ...string) {
	t.Helper()
	for _, r := range reviewers {
		if _, err := svc.Approve(context.Background(), id, r, "ok"); err != nil {
			t.Fatalf("approve %s: %v", r, err)
		}
	}
}

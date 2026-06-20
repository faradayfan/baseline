package promotions

import "testing"

func pr(proposer string, required int, reviews ...Review) PromotionRequest {
	return PromotionRequest{Proposer: proposer, RequiredApprovals: required, Reviews: reviews}
}

func approve(by string) Review { return Review{Reviewer: by, Decision: DecisionApprove} }

// TestSeparationOfDuties asserts §14.6: the proposer's own approval never counts,
// at any policy.
func TestSeparationOfDuties(t *testing.T) {
	// Proposer "alice" approves her own + one real reviewer; required = 2.
	p := pr("alice", 2, approve("alice"), approve("bob"))
	if got := p.ApprovalCount(); got != 1 {
		t.Errorf("ApprovalCount = %d, want 1 (alice excluded)", got)
	}
	if p.HasEnoughApprovals() {
		t.Error("must NOT be approved: only 1 valid approver for required=2")
	}

	// Even required=1, a sole self-approval is insufficient.
	solo := pr("alice", 1, approve("alice"))
	if solo.ApprovalCount() != 0 || solo.HasEnoughApprovals() {
		t.Error("self-approval alone must never satisfy approval")
	}
}

// TestAnyNDistinct asserts §14.1 + §8.4: distinct reviewers, order-independent,
// duplicates don't double-count.
func TestAnyNDistinct(t *testing.T) {
	// Two distinct reviewers, required=2 → approved.
	p := pr("alice", 2, approve("bob"), approve("carol"))
	if !p.HasEnoughApprovals() {
		t.Error("two distinct approvers should satisfy required=2")
	}

	// Same reviewer twice counts once.
	dup := pr("alice", 2, approve("bob"), approve("bob"))
	if dup.ApprovalCount() != 1 {
		t.Errorf("duplicate approver counted %d times, want 1", dup.ApprovalCount())
	}
	if dup.HasEnoughApprovals() {
		t.Error("one distinct approver must not satisfy required=2")
	}

	// Non-approve decisions don't count.
	mixed := pr("alice", 1, Review{Reviewer: "bob", Decision: DecisionRequestChanges})
	if mixed.ApprovalCount() != 0 {
		t.Error("request_changes is not an approval")
	}
}

func TestAlreadyApprovedBy(t *testing.T) {
	p := pr("alice", 2, approve("bob"))
	if !p.AlreadyApprovedBy("bob") {
		t.Error("bob already approved")
	}
	if p.AlreadyApprovedBy("carol") {
		t.Error("carol has not approved")
	}
}

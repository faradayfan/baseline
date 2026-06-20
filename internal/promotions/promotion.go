// Package promotions implements the review workflow that moves a candidate
// statement toward becoming an active fact (§4.2, §8). It owns conflict
// detection, the any-N-distinct-reviewer approval rule with separation of
// duties, and atomic supersession on approval.
package promotions

import (
	"time"

	"github.com/google/uuid"
)

// State is a promotion request's workflow state (§4.2).
type State string

const (
	StatePending          State = "pending"
	StateInReview         State = "in_review"
	StateChangesRequested State = "changes_requested"
	StateApproved         State = "approved"
	StateRejected         State = "rejected"
)

// Decision is a single reviewer's verdict, stored in the reviews[] array.
type Decision string

const (
	DecisionApprove        Decision = "approve"
	DecisionReject         Decision = "reject"
	DecisionRequestChanges Decision = "request_changes"
)

// Review is one entry in a promotion's review log (§4.2).
type Review struct {
	Reviewer string    `json:"reviewer"`
	Decision Decision  `json:"decision"`
	Comment  string    `json:"comment,omitempty"`
	At       time.Time `json:"at"`
}

// PromotionRequest drives a fact through review (§4.2).
type PromotionRequest struct {
	ID                 uuid.UUID  `json:"id"`
	FactID             uuid.UUID  `json:"fact_id"`
	TargetNamespaceID  uuid.UUID  `json:"target_namespace_id"`
	ProposedStatement  string     `json:"proposed_statement"`
	State              State      `json:"state"`
	CandidateMemoryIDs []string   `json:"candidate_memory_ids,omitempty"`
	Proposer           string     `json:"proposer"`
	Reviews            []Review   `json:"reviews"`
	RequiredApprovals  int        `json:"required_approvals"` // snapshot of policy at create
	ConflictWith       *uuid.UUID `json:"conflict_with,omitempty"`
	IdempotencyKey     *string    `json:"idempotency_key,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// distinctApprovers returns the set of reviewers who approved, EXCLUDING the
// proposer (separation of duties, §14.6) — even if the proposer somehow recorded
// an approval, it never counts.
func (p PromotionRequest) distinctApprovers() map[string]struct{} {
	out := map[string]struct{}{}
	for _, r := range p.Reviews {
		if r.Decision != DecisionApprove {
			continue
		}
		if r.Reviewer == p.Proposer {
			continue // SoD: proposer cannot approve their own
		}
		out[r.Reviewer] = struct{}{}
	}
	return out
}

// ApprovalCount is the number of distinct, non-proposer approvers (§8.4).
func (p PromotionRequest) ApprovalCount() int { return len(p.distinctApprovers()) }

// HasEnoughApprovals reports whether the any-N threshold is met (§14.1).
func (p PromotionRequest) HasEnoughApprovals() bool {
	return p.ApprovalCount() >= p.RequiredApprovals
}

// AlreadyReviewed reports whether a reviewer has already recorded a decision,
// used to keep approvals idempotent and the distinct-reviewer count honest.
func (p PromotionRequest) AlreadyApprovedBy(reviewer string) bool {
	for _, r := range p.Reviews {
		if r.Reviewer == reviewer && r.Decision == DecisionApprove {
			return true
		}
	}
	return false
}

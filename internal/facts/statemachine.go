package facts

// Transition is a named lifecycle action on a fact (§5).
type Transition string

const (
	TransitionSubmit         Transition = "submit"          // proposed -> in_review
	TransitionWithdraw       Transition = "withdraw"        // proposed -> (terminal: rejected)
	TransitionApprove        Transition = "approve"         // in_review -> active
	TransitionReject         Transition = "reject"          // in_review -> rejected
	TransitionRequestChanges Transition = "request_changes" // in_review -> proposed
	TransitionSupersede      Transition = "supersede"       // active -> superseded (system)
	TransitionRevoke         Transition = "revoke"          // active -> revoked
	TransitionExpire         Transition = "expire"          // active -> expired (reaper)
)

// allowed maps each transition to its (from -> to) states (§5 diagram).
var allowed = map[Transition]struct{ from, to Status }{
	TransitionSubmit:         {StatusProposed, StatusInReview},
	TransitionWithdraw:       {StatusProposed, StatusRejected},
	TransitionApprove:        {StatusInReview, StatusActive},
	TransitionReject:         {StatusInReview, StatusRejected},
	TransitionRequestChanges: {StatusInReview, StatusProposed},
	TransitionSupersede:      {StatusActive, StatusSuperseded},
	TransitionRevoke:         {StatusActive, StatusRevoked},
	TransitionExpire:         {StatusActive, StatusExpired},
}

// terminal states have no outgoing transitions.
var terminal = map[Status]bool{
	StatusSuperseded: true,
	StatusRevoked:    true,
	StatusExpired:    true,
	StatusRejected:   true,
}

// CanTransition reports whether t is legal from the current status, and the
// resulting status if so. It is the single authority on lifecycle legality;
// RBAC (who may trigger) is enforced separately by the caller.
func CanTransition(current Status, t Transition) (Status, bool) {
	rule, ok := allowed[t]
	if !ok || rule.from != current {
		return "", false
	}
	return rule.to, true
}

// IsTerminal reports whether a status is final (no further transitions).
func IsTerminal(s Status) bool { return terminal[s] }

package facts

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from   Status
		t      Transition
		wantTo Status
		wantOK bool
	}{
		{StatusProposed, TransitionSubmit, StatusInReview, true},
		{StatusProposed, TransitionWithdraw, StatusRejected, true},
		{StatusInReview, TransitionApprove, StatusActive, true},
		{StatusInReview, TransitionReject, StatusRejected, true},
		{StatusInReview, TransitionRequestChanges, StatusProposed, true},
		{StatusActive, TransitionRevoke, StatusRevoked, true},
		{StatusActive, TransitionSupersede, StatusSuperseded, true},
		{StatusActive, TransitionExpire, StatusExpired, true},

		// Illegal: wrong source state.
		{StatusProposed, TransitionApprove, "", false},
		{StatusActive, TransitionSubmit, "", false},
		{StatusInReview, TransitionRevoke, "", false},
		// Illegal: from terminal states.
		{StatusRejected, TransitionSubmit, "", false},
		{StatusSuperseded, TransitionRevoke, "", false},
		{StatusExpired, TransitionApprove, "", false},
		{StatusRevoked, TransitionApprove, "", false},
	}
	for _, tt := range tests {
		to, ok := CanTransition(tt.from, tt.t)
		if ok != tt.wantOK || to != tt.wantTo {
			t.Errorf("CanTransition(%s, %s) = (%s, %v), want (%s, %v)",
				tt.from, tt.t, to, ok, tt.wantTo, tt.wantOK)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	for _, s := range []Status{StatusSuperseded, StatusRevoked, StatusExpired, StatusRejected} {
		if !IsTerminal(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []Status{StatusProposed, StatusInReview, StatusActive} {
		if IsTerminal(s) {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

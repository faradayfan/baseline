package namespaces

import "testing"

func TestDefaultPolicy(t *testing.T) {
	tests := []struct {
		kind          Kind
		wantApprovals int
		wantAuto      bool
		wantEngine    string
	}{
		{KindOrg, 2, false, ""},
		{KindTeam, 1, true, "simple/v1"},
		{KindProject, 1, false, ""},
		{KindUser, 1, false, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			p := DefaultPolicy(tt.kind)
			if p.RequiredApprovals != tt.wantApprovals {
				t.Errorf("RequiredApprovals = %d, want %d", p.RequiredApprovals, tt.wantApprovals)
			}
			if tt.wantAuto {
				if p.AutoPromote == nil {
					t.Fatalf("expected auto-promote engine, got nil")
				}
				if p.AutoPromote.Engine != tt.wantEngine {
					t.Errorf("engine = %q, want %q", p.AutoPromote.Engine, tt.wantEngine)
				}
			} else if p.AutoPromote != nil {
				t.Errorf("expected no auto-promote, got %+v", p.AutoPromote)
			}
		})
	}
}

func TestPolicyIsZero(t *testing.T) {
	if !(Policy{}).isZero() {
		t.Error("empty policy should be zero")
	}
	if (Policy{RequiredApprovals: 1}).isZero() {
		t.Error("policy with approvals is not zero")
	}
	if (Policy{AutoPromote: &AutoPromote{Engine: "simple/v1"}}).isZero() {
		t.Error("policy with auto-promote is not zero")
	}
}

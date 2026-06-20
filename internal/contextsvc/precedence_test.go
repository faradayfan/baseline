package contextsvc

import "testing"

// TestOutranks asserts the §6 precedence ordering and the authoritative override
// (§14.9), as pure logic.
func TestOutranks(t *testing.T) {
	tests := []struct {
		name                       string
		aAuth                      bool
		aKind                      string
		bAuth                      bool
		bKind                      string
		want                       bool
	}{
		{"user beats org", false, "user", false, "org", true},
		{"project beats team", false, "project", false, "team", true},
		{"team beats org", false, "team", false, "org", true},
		{"org does not beat user", false, "org", false, "user", false},
		{"authoritative org beats non-auth user", true, "org", false, "user", true},
		{"non-auth user does not beat authoritative org", false, "user", true, "org", false},
		{"both authoritative falls back to specificity", true, "user", true, "org", true},
		{"same kind, same auth → not strictly greater", false, "team", false, "team", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outranks(tt.aAuth, tt.aKind, tt.bAuth, tt.bKind); got != tt.want {
				t.Errorf("outranks = %v, want %v", got, tt.want)
			}
		})
	}
}

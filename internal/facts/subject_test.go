package facts

import "testing"

func TestCanonicalKey(t *testing.T) {
	tests := []struct {
		name string
		subj Subject
		want string
	}{
		{"type only -> global scope", Subject{Type: "build.command"}, "build.command:global"},
		{"type + scope", Subject{Type: "build.command", Scope: "service-foo"}, "build.command:service-foo"},
		{
			"with one qualifier",
			Subject{Type: "build.command", Scope: "service-foo", Qualifiers: map[string]string{"env": "prod"}},
			"build.command:service-foo:env=prod",
		},
		{
			"qualifiers sorted by key, not insertion order",
			Subject{Type: "t", Scope: "s", Qualifiers: map[string]string{"z": "1", "a": "2", "m": "3"}},
			"t:s:a=2:m=3:z=1",
		},
		{
			"case-folded and trimmed type/scope/qual-keys",
			Subject{Type: "  Build.Command  ", Scope: " Service-FOO ", Qualifiers: map[string]string{" ENV ": "Prod"}},
			"build.command:service-foo:env=Prod", // values keep case, keys lower-cased
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.subj.CanonicalKey(); got != tt.want {
				t.Errorf("CanonicalKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCanonicalKey_Deterministic asserts §14.16: identical subjects yield
// identical keys across repeated, independent derivations.
func TestCanonicalKey_Deterministic(t *testing.T) {
	mk := func() Subject {
		return Subject{
			Type:       "deploy.target",
			Scope:      "api",
			Qualifiers: map[string]string{"region": "us-east-1", "env": "prod"},
		}
	}
	first := mk().CanonicalKey()
	for i := 0; i < 100; i++ {
		if got := mk().CanonicalKey(); got != first {
			t.Fatalf("non-deterministic: run %d = %q, first = %q", i, got, first)
		}
	}
	if first != "deploy.target:api:env=prod:region=us-east-1" {
		t.Errorf("unexpected key: %q", first)
	}
}

// TestCanonicalKey_PhrasingIndependent asserts §14.16: the key depends only on
// the subject, never the statement — two differently-worded facts with the same
// subject collapse to one key (so an update supersedes rather than duplicates).
func TestCanonicalKey_PhrasingIndependent(t *testing.T) {
	a := Subject{Type: "build.command", Scope: "svc"}
	b := Subject{Type: "build.command", Scope: "svc"} // same subject, imagine different statements
	if a.CanonicalKey() != b.CanonicalKey() {
		t.Error("same subject must produce same key regardless of statement phrasing")
	}
}

func TestSubject_Valid(t *testing.T) {
	if !(Subject{Type: "x"}).Valid() {
		t.Error("non-empty type is valid")
	}
	if (Subject{Type: "   "}).Valid() {
		t.Error("whitespace-only type is invalid")
	}
	if (Subject{Scope: "x"}).Valid() {
		t.Error("missing type is invalid")
	}
}

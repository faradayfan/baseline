package simple

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/faradayfan/baseline/internal/autopromote"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		rules   string
		wantErr bool
	}{
		{"valid eq", `{"rules":[{"all":[{"field":"actor.type","op":"eq","value":"human"}]}]}`, false},
		{"valid in", `{"rules":[{"all":[{"field":"actor.type","op":"in","value":["human","team-agent"]}]}]}`, false},
		{"valid metadata field", `{"rules":[{"all":[{"field":"metadata.team","op":"eq","value":"eng"}]}]}`, false},
		{"empty", ``, true},
		{"empty rules list", `{"rules":[]}`, true},
		{"rule with no conditions", `{"rules":[{"all":[]}]}`, true},
		{"non-whitelisted field", `{"rules":[{"all":[{"field":"statement","op":"eq","value":"x"}]}]}`, true},
		{"unknown op", `{"rules":[{"all":[{"field":"actor.type","op":"regex","value":"x"}]}]}`, true},
		{"missing value", `{"rules":[{"all":[{"field":"actor.type","op":"eq"}]}]}`, true},
		{"unknown top-level field (DisallowUnknownFields)", `{"rules":[],"extra":1}`, true},
		{"malformed json", `{"rules":`, true},
	}
	e := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.Validate(json.RawMessage(tt.rules))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%s) err = %v, wantErr %v", tt.rules, err, tt.wantErr)
			}
		})
	}
}

func TestEvaluate(t *testing.T) {
	// Auto-promote a merged-PR fact authored by a human or team-agent.
	rules := json.RawMessage(`{"rules":[{"all":[
		{"field":"provenance.origin_type","op":"eq","value":"merged_pr"},
		{"field":"actor.type","op":"in","value":["human","team-agent"]}
	]}]}`)

	tests := []struct {
		name string
		cand autopromote.Candidate
		want bool
	}{
		{
			"matches: merged_pr + human",
			autopromote.Candidate{ProvenanceOriginType: "merged_pr", ActorType: "human"},
			true,
		},
		{
			"matches: merged_pr + team-agent",
			autopromote.Candidate{ProvenanceOriginType: "merged_pr", ActorType: "team-agent"},
			true,
		},
		{
			"no match: wrong origin (AND fails)",
			autopromote.Candidate{ProvenanceOriginType: "manual", ActorType: "human"},
			false,
		},
		{
			"no match: wrong actor (in fails)",
			autopromote.Candidate{ProvenanceOriginType: "merged_pr", ActorType: "external-bot"},
			false,
		},
	}
	e := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := e.Evaluate(context.Background(), tt.cand, rules)
			if err != nil {
				t.Fatalf("Evaluate err: %v", err)
			}
			if d.AutoPromote != tt.want {
				t.Errorf("AutoPromote = %v, want %v", d.AutoPromote, tt.want)
			}
			if d.AutoPromote && len(d.MatchedRule) == 0 {
				t.Error("a positive decision must record the matched rule for attribution")
			}
		})
	}
}

// TestEvaluate_ORAcrossRules: matching any rule promotes.
func TestEvaluate_ORAcrossRules(t *testing.T) {
	rules := json.RawMessage(`{"rules":[
		{"all":[{"field":"actor.type","op":"eq","value":"human"}]},
		{"all":[{"field":"tags","op":"eq","value":"trusted"}]}
	]}`)
	e := New()
	// Matches the second rule via tags.
	d, _ := e.Evaluate(context.Background(), autopromote.Candidate{ActorType: "bot", Tags: []string{"trusted"}}, rules)
	if !d.AutoPromote {
		t.Error("should match second rule by tag")
	}
}

func TestEvaluate_MetadataField(t *testing.T) {
	rules := json.RawMessage(`{"rules":[{"all":[{"field":"metadata.env","op":"eq","value":"prod"}]}]}`)
	e := New()
	d, _ := e.Evaluate(context.Background(),
		autopromote.Candidate{Metadata: map[string]any{"env": "prod"}}, rules)
	if !d.AutoPromote {
		t.Error("should match metadata.env=prod")
	}
	d, _ = e.Evaluate(context.Background(),
		autopromote.Candidate{Metadata: map[string]any{"env": "dev"}}, rules)
	if d.AutoPromote {
		t.Error("must not match metadata.env=dev")
	}
}

// TestEvaluate_Deterministic asserts §14.13: identical candidate + rules + engine
// version yield the identical decision across repeated runs.
func TestEvaluate_Deterministic(t *testing.T) {
	rules := json.RawMessage(`{"rules":[{"all":[
		{"field":"provenance.origin_type","op":"eq","value":"merged_pr"},
		{"field":"tags","op":"in","value":["a","b","c"]}
	]}]}`)
	cand := autopromote.Candidate{ProvenanceOriginType: "merged_pr", Tags: []string{"b"}}
	e := New()
	first, _ := e.Evaluate(context.Background(), cand, rules)
	for i := 0; i < 50; i++ {
		d, _ := e.Evaluate(context.Background(), cand, rules)
		if d.AutoPromote != first.AutoPromote || string(d.MatchedRule) != string(first.MatchedRule) {
			t.Fatalf("non-deterministic at run %d", i)
		}
	}
	if !first.AutoPromote {
		t.Error("expected promote")
	}
}

func TestID(t *testing.T) {
	if New().ID() != "simple/v1" {
		t.Errorf("ID = %q, want simple/v1", New().ID())
	}
}

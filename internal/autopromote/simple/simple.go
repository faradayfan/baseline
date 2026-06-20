// Package simple implements the simple/v1 auto-promotion engine (§7.4): a tiny,
// whitelisted predicate list with no expression language and no code execution.
//
// rules shape:
//
//	{ "rules": [ { "all": [ {"field": <whitelisted>, "op": "eq"|"in", "value": <scalar|array>}, ... ] }, ... ] }
//
// A candidate auto-promotes if it matches ANY rule (OR); within a rule, ALL
// conditions must hold (AND). Anything outside the whitelist, any unknown op, or
// malformed rules causes Validate to reject (fail closed).
package simple

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/faradayfan/baseline/internal/autopromote"
)

// ID is the engine identifier. Pinned, immutable: a breaking change becomes
// a new package/ID (simple/v2), never a mutation of this one.
const ID = "simple/v1"

// whitelistedFields are the only fields conditions may reference (§7.4). The
// metadata.<key> form is matched by prefix.
var whitelistedFields = map[string]bool{
	"provenance.origin_type": true,
	"provenance.source.kind": true,
	"actor.type":             true,
	"tags":                   true,
}

const metadataPrefix = "metadata."

// Engine is the simple/v1 implementation. It is stateless and deterministic.
type Engine struct{}

// New returns a simple/v1 engine.
func New() Engine { return Engine{} }

func (Engine) ID() string { return ID }

// ruleSet is the parsed rules document.
type ruleSet struct {
	Rules []rule `json:"rules"`
}

type rule struct {
	All []condition `json:"all"`
}

type condition struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
}

// Validate parses rules and rejects anything malformed, any non-whitelisted
// field, or any unknown op — at policy-write time (§14.15).
func (Engine) Validate(rules json.RawMessage) error {
	if len(rules) == 0 {
		return fmt.Errorf("simple/v1: empty rules")
	}
	var rs ruleSet
	dec := json.NewDecoder(bytes.NewReader(rules))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rs); err != nil {
		return fmt.Errorf("simple/v1: malformed rules: %w", err)
	}
	if len(rs.Rules) == 0 {
		return fmt.Errorf("simple/v1: rules list is empty")
	}
	for ri, r := range rs.Rules {
		if len(r.All) == 0 {
			return fmt.Errorf("simple/v1: rule %d has no conditions", ri)
		}
		for ci, c := range r.All {
			if !fieldAllowed(c.Field) {
				return fmt.Errorf("simple/v1: rule %d cond %d: field %q not whitelisted", ri, ci, c.Field)
			}
			if c.Op != "eq" && c.Op != "in" {
				return fmt.Errorf("simple/v1: rule %d cond %d: unknown op %q", ri, ci, c.Op)
			}
			if len(c.Value) == 0 {
				return fmt.Errorf("simple/v1: rule %d cond %d: missing value", ri, ci)
			}
		}
	}
	return nil
}

// Evaluate returns AutoPromote=true if the candidate matches any rule. It
// re-validates defensively and fails closed on any parse error.
func (e Engine) Evaluate(_ context.Context, c autopromote.Candidate, rules json.RawMessage) (autopromote.Decision, error) {
	if err := e.Validate(rules); err != nil {
		return autopromote.Decision{AutoPromote: false}, err
	}
	var rs ruleSet
	if err := json.Unmarshal(rules, &rs); err != nil {
		return autopromote.Decision{AutoPromote: false}, err
	}

	for _, r := range rs.Rules {
		if ruleMatches(r, c) {
			matched, _ := json.Marshal(r)
			return autopromote.Decision{
				AutoPromote: true,
				Reason:      "matched simple/v1 rule",
				MatchedRule: matched,
			}, nil
		}
	}
	return autopromote.Decision{AutoPromote: false, Reason: "no rule matched"}, nil
}

func ruleMatches(r rule, c autopromote.Candidate) bool {
	for _, cond := range r.All {
		if !conditionMatches(cond, c) {
			return false // AND: any failing condition fails the rule
		}
	}
	return true
}

func conditionMatches(cond condition, c autopromote.Candidate) bool {
	switch cond.Op {
	case "eq":
		var want string
		if err := json.Unmarshal(cond.Value, &want); err != nil {
			return false
		}
		return fieldHasScalar(cond.Field, want, c)
	case "in":
		var want []string
		if err := json.Unmarshal(cond.Value, &want); err != nil {
			return false
		}
		for _, v := range want {
			if fieldHasScalar(cond.Field, v, c) {
				return true
			}
		}
		return false
	}
	return false
}

// fieldHasScalar reports whether the whitelisted field equals/contains want.
// For tags (a list), it means membership; for metadata.<key>, equality on the
// stringified value.
func fieldHasScalar(field, want string, c autopromote.Candidate) bool {
	switch field {
	case "provenance.origin_type":
		return c.ProvenanceOriginType == want
	case "provenance.source.kind":
		return c.ProvenanceSourceKind == want
	case "actor.type":
		return c.ActorType == want
	case "tags":
		for _, t := range c.Tags {
			if t == want {
				return true
			}
		}
		return false
	default:
		if key, ok := metadataKey(field); ok {
			v, present := c.Metadata[key]
			return present && fmt.Sprintf("%v", v) == want
		}
		return false
	}
}

func fieldAllowed(field string) bool {
	if whitelistedFields[field] {
		return true
	}
	_, ok := metadataKey(field)
	return ok
}

func metadataKey(field string) (string, bool) {
	if len(field) > len(metadataPrefix) && field[:len(metadataPrefix)] == metadataPrefix {
		return field[len(metadataPrefix):], true
	}
	return "", false
}

// Package facts owns the fact domain: the structured subject and its canonical
// key (§4.6), the lifecycle state machine (§5), and the fact repository.
package facts

import (
	"sort"
	"strings"
)

// Subject is a fact's structured identity, supplied at propose time. It is the
// SOLE source of canonical identity — never parsed from the free-text statement
// (§4.6). Type is required; Scope defaults to "global"; Qualifiers add extra
// identity dimensions.
type Subject struct {
	Type       string            `json:"type"`
	Scope      string            `json:"scope,omitempty"`
	Qualifiers map[string]string `json:"qualifiers,omitempty"`
}

// CanonicalKey derives the canonical key from a subject. It is a PURE,
// DETERMINISTIC function and the single path through which canonical_key is ever
// produced (§4.6, conformance §14.16/17):
//
//  1. lower-case and trim type and scope (scope defaults to "global");
//  2. append qualifiers as k=v pairs, keys lower-cased/trimmed and sorted;
//  3. join with ':'.
//
// e.g. {build.command, service-foo}            -> "build.command:service-foo"
//      {build.command, service-foo, env=prod}  -> "build.command:service-foo:env=prod"
//
// Identical subjects always yield identical keys, in any process. Differently
// worded statements about the same subject collapse to one key (driving
// supersession, not duplication).
func (s Subject) CanonicalKey() string {
	typ := strings.ToLower(strings.TrimSpace(s.Type))
	scope := strings.ToLower(strings.TrimSpace(s.Scope))
	if scope == "" {
		scope = "global"
	}

	parts := []string{typ, scope}

	if len(s.Qualifiers) > 0 {
		quals := make([]string, 0, len(s.Qualifiers))
		for k, v := range s.Qualifiers {
			key := strings.ToLower(strings.TrimSpace(k))
			val := strings.TrimSpace(v)
			quals = append(quals, key+"="+val)
		}
		sort.Strings(quals)
		parts = append(parts, quals...)
	}

	return strings.Join(parts, ":")
}

// Valid reports whether the subject is well-formed enough to derive a key:
// Type must be non-empty after trimming. (Scope and qualifiers are optional.)
func (s Subject) Valid() bool {
	return strings.TrimSpace(s.Type) != ""
}

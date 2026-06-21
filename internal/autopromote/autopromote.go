// Package autopromote defines the pluggable, versioned auto-promotion engine
// contract (§7.4) and a registry mapping engine IDs to implementations.
//
// Baseline depends on the Engine interface, not on a specific rule language.
// Each engine is identified by a "family/vN" ID and is immutable: simple/v2 may
// change semantics arbitrarily because it is a distinct version, and registering
// it never affects namespaces pinned to simple/v1 (no silent migration, §14.14).
//
// Safety invariants every engine must uphold (§7.4, §14.11–15):
//   - Fail closed: any error/invalid rules ⇒ no auto-promotion (caller falls
//     back to human review). An engine never auto-approves on uncertainty.
//   - Deterministic: same candidate + rules + version ⇒ same decision.
//   - A Decision NEVER carries an approver identity — attribution is engine:<ID>.
package autopromote

import (
	"context"
	"encoding/json"
	"fmt"
)

// Candidate is the neutral, engine-agnostic view of a proposal under evaluation.
// Engines read only these fields; they cannot reach into Baseline's internals.
type Candidate struct {
	// ProvenanceOriginType is provenance.origin_type (e.g. "merged_pr").
	ProvenanceOriginType string
	// ProvenanceSourceKind is provenance.source.kind.
	ProvenanceSourceKind string
	// ActorType is the proposing actor's type (e.g. "human", "team-agent").
	ActorType string
	// Tags are the candidate fact's tags.
	Tags []string
	// Metadata is the candidate fact's metadata map (queried as metadata.<key>).
	Metadata map[string]any
}

// Decision is an engine's verdict. It never names an approver — auto-promotions
// are attributed to engine:<ID>, never to a person.
type Decision struct {
	AutoPromote bool
	Reason      string
	// MatchedRule is an engine-specific description of what matched, recorded in
	// the audit detail for traceability (§14.12). Empty when nothing matched.
	MatchedRule json.RawMessage
}

// Engine is the contract Baseline needs from an auto-promotion implementation
// (§7.4). Implementations must be pure/deterministic and fail closed.
type Engine interface {
	ID() string                           // e.g. "simple/v1"
	Validate(rules json.RawMessage) error // checked at policy-write time
	Evaluate(ctx context.Context, c Candidate, rules json.RawMessage) (Decision, error)
}

// Registry maps engine IDs to implementations. It is constructed once at startup
// with the known engines and is read-only thereafter.
type Registry struct {
	engines map[string]Engine
}

// NewRegistry builds a registry from the given engines, keyed by ID().
func NewRegistry(engines ...Engine) *Registry {
	m := make(map[string]Engine, len(engines))
	for _, e := range engines {
		m[e.ID()] = e
	}
	return &Registry{engines: m}
}

// ErrUnknownEngine is returned for an unregistered engine ID — a policy that
// references it is invalid and must be rejected at write time (fail closed,
// §14.15).
type ErrUnknownEngine struct{ ID string }

func (e ErrUnknownEngine) Error() string { return fmt.Sprintf("unknown auto-promote engine %q", e.ID) }

// Get returns the engine for id, or ErrUnknownEngine.
func (r *Registry) Get(id string) (Engine, error) {
	e, ok := r.engines[id]
	if !ok {
		return nil, ErrUnknownEngine{ID: id}
	}
	return e, nil
}

// ValidatePolicy checks that engineID is registered and its rules pass the
// engine's Validate. Call this at policy-write time; a failure blocks the write
// so a namespace can never hold rules its engine cannot interpret (§14.15).
func (r *Registry) ValidatePolicy(engineID string, rules json.RawMessage) error {
	e, err := r.Get(engineID)
	if err != nil {
		return err
	}
	return e.Validate(rules)
}

// Evaluate looks up the pinned engine and evaluates the candidate. It FAILS
// CLOSED: an unknown engine or any engine error yields AutoPromote=false with no
// error surfaced as a promotion (the caller treats a false decision as "go to
// human review"). The returned error is for logging/observability only.
func (r *Registry) Evaluate(ctx context.Context, engineID string, c Candidate, rules json.RawMessage) (Decision, error) {
	e, err := r.Get(engineID)
	if err != nil {
		return Decision{AutoPromote: false}, err
	}
	d, err := e.Evaluate(ctx, c, rules)
	if err != nil {
		// Fail closed: never auto-promote on engine error (§14.11).
		return Decision{AutoPromote: false}, err
	}
	return d, nil
}

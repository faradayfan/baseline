package autopromote_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/faradayfan/baseline/internal/autopromote"
	"github.com/faradayfan/baseline/internal/autopromote/simple"
)

func TestRegistry_UnknownEngineFailsClosed(t *testing.T) {
	reg := autopromote.NewRegistry(simple.New())

	// ValidatePolicy on an unknown engine → invalid (§14.15).
	err := reg.ValidatePolicy("cel/v1", json.RawMessage(`{}`))
	var unknown autopromote.ErrUnknownEngine
	if !errors.As(err, &unknown) {
		t.Errorf("ValidatePolicy unknown engine err = %v, want ErrUnknownEngine", err)
	}

	// Evaluate on an unknown engine fails closed: AutoPromote=false (§14.11).
	d, err := reg.Evaluate(context.Background(), "cel/v1", autopromote.Candidate{}, nil)
	if err == nil {
		t.Error("expected an error for unknown engine (for logging)")
	}
	if d.AutoPromote {
		t.Error("unknown engine must never auto-promote (fail closed)")
	}
}

// TestRegistry_VersionIsolation asserts §14.14: a registry holding simple/v1
// resolves only that version; a hypothetical simple/v2 (not registered) is
// unknown and cannot change v1 decisions. We model it by confirming the
// registry dispatches strictly by ID.
func TestRegistry_VersionIsolation(t *testing.T) {
	reg := autopromote.NewRegistry(simple.New())

	if _, err := reg.Get("simple/v1"); err != nil {
		t.Fatalf("simple/v1 should be registered: %v", err)
	}
	if _, err := reg.Get("simple/v2"); err == nil {
		t.Error("simple/v2 is not registered and must not resolve to v1")
	}
}

func TestRegistry_ValidatePolicy_DelegatesToEngine(t *testing.T) {
	reg := autopromote.NewRegistry(simple.New())
	// Invalid rules for simple/v1 are rejected at policy-write time.
	if err := reg.ValidatePolicy("simple/v1", json.RawMessage(`{"rules":[{"all":[{"field":"nope","op":"eq","value":"x"}]}]}`)); err == nil {
		t.Error("non-whitelisted field should be rejected")
	}
	// Valid rules pass.
	if err := reg.ValidatePolicy("simple/v1", json.RawMessage(`{"rules":[{"all":[{"field":"actor.type","op":"eq","value":"human"}]}]}`)); err != nil {
		t.Errorf("valid rules rejected: %v", err)
	}
}

package audit_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/faradayfan/baseline/internal/audit"
	"github.com/faradayfan/baseline/internal/storetest"
)

// TestWrite_AppendsRow verifies a single immutable event is written with detail
// marshaled to jsonb and optional states stored as NULL when empty.
func TestWrite_AppendsRow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ctx := context.Background()
	id := uuid.New()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Write(ctx, tx, audit.Event{
		Principal:   "alice",
		Action:      "fact.proposed",
		SubjectType: "promotion",
		SubjectID:   id,
		ToState:     "pending",
		Detail:      map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var (
		principal, action, subjType, toState string
		fromState                            *string
		detail                               map[string]any
	)
	err = pool.QueryRow(ctx, `
		SELECT principal, action, subject_type, from_state, to_state, detail
		FROM audit_events WHERE subject_id = $1`, id).
		Scan(&principal, &action, &subjType, &fromState, &toState, &detail)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if principal != "alice" || action != "fact.proposed" || subjType != "promotion" || toState != "pending" {
		t.Errorf("unexpected row: %s %s %s ->%s", principal, action, subjType, toState)
	}
	if fromState != nil {
		t.Errorf("empty from_state should be NULL, got %v", *fromState)
	}
	if detail["k"] != "v" {
		t.Errorf("detail not persisted: %v", detail)
	}
}

// TestWrite_NilDetailIsEmptyObject verifies a nil Detail stores '{}' (the column
// is NOT NULL DEFAULT '{}').
func TestWrite_NilDetailIsEmptyObject(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ctx := context.Background()
	id := uuid.New()

	tx, _ := pool.Begin(ctx)
	if err := audit.Write(ctx, tx, audit.Event{
		Principal: "system", Action: "fact.expired", SubjectType: "fact", SubjectID: id,
		FromState: "active", ToState: "expired",
	}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	var detail map[string]any
	if err := pool.QueryRow(ctx,
		`SELECT detail FROM audit_events WHERE subject_id = $1`, id).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if len(detail) != 0 {
		t.Errorf("nil detail should be empty object, got %v", detail)
	}
}

// TestWrite_RollbackLeavesNothing confirms audit writes participate in the
// caller's transaction — a rollback discards the event (atomic with its transition).
func TestWrite_RollbackLeavesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ctx := context.Background()
	id := uuid.New()

	tx, _ := pool.Begin(ctx)
	if err := audit.Write(ctx, tx, audit.Event{
		Principal: "x", Action: "a", SubjectType: "fact", SubjectID: id,
	}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE subject_id = $1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rolled-back audit write should leave no row, got %d", count)
	}
}

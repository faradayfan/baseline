// Package audit is the append-only event writer (§4.5). Every fact/promotion
// state transition writes exactly one immutable AuditEvent (§14.5). Rows are
// never updated or deleted.
//
// The writer takes a pgx transaction so the audit row is committed atomically
// with the state change it records — there is no way to perform a transition
// without its audit event, nor to leave an orphan event behind.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event is one append-only audit record.
type Event struct {
	Principal   string
	Action      string
	SubjectType string // "fact" | "promotion" | "namespace" | "membership"
	SubjectID   uuid.UUID
	FromState   string // optional
	ToState     string // optional
	Detail      any    // marshaled to jsonb; diff / comment / policy snapshot
}

// Write appends one event within the given transaction. Call it on the same tx
// as the transition; the caller's commit makes both durable together.
func Write(ctx context.Context, tx pgx.Tx, e Event) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return fmt.Errorf("audit: marshal detail: %w", err)
	}
	if e.Detail == nil {
		detail = []byte(`{}`)
	}

	const q = `
		INSERT INTO audit_events
			(principal, action, subject_type, subject_id, from_state, to_state, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = tx.Exec(ctx, q,
		e.Principal, e.Action, e.SubjectType, e.SubjectID,
		nullable(e.FromState), nullable(e.ToState), detail)
	if err != nil {
		return fmt.Errorf("audit: write %s: %w", e.Action, err)
	}
	return nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

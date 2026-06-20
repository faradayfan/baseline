package facts

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/audit"
)

// Service wraps fact operations that must be transactional with audit (e.g.
// revoke). Pure reads go straight through the package funcs.
type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Revoke transitions an active fact to revoked under optimistic concurrency
// (§14.8) and writes one audit event, atomically. A stale expectedVersion yields
// ErrVersionConflict and no write.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID, expectedVersion int, principal string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("facts: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := RevokeWithVersion(ctx, tx, id, expectedVersion); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Event{
		Principal: principal, Action: "fact.revoked", SubjectType: "fact",
		SubjectID: id, FromState: string(StatusActive), ToState: string(StatusRevoked),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// History returns the audit trail for a fact, oldest first (§9 GET /facts/{id}/history).
func (s *Service) History(ctx context.Context, id uuid.UUID) ([]audit.Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT principal, action, subject_type, subject_id,
		       coalesce(from_state,''), coalesce(to_state,'')
		FROM audit_events WHERE subject_id = $1 ORDER BY at`, id)
	if err != nil {
		return nil, fmt.Errorf("facts: history: %w", err)
	}
	defer rows.Close()
	var out []audit.Event
	for rows.Next() {
		var e audit.Event
		if err := rows.Scan(&e.Principal, &e.Action, &e.SubjectType, &e.SubjectID, &e.FromState, &e.ToState); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

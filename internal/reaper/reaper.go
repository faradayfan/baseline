// Package reaper expires facts whose validity window has passed (§13 staleness).
// It runs as a periodic job (a Kubernetes CronJob in deployment) and transitions
// active facts past their valid_to to `expired`, writing one audit event per
// transition (§14.5) so the change is traceable like any other.
package reaper

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/audit"
)

// Result reports what a reap pass did.
type Result struct {
	Expired      int // facts transitioned active -> expired this pass
	ExpiringSoon int // active facts whose valid_to is within the next 24h
}

// Reaper expires stale facts.
type Reaper struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Reaper { return &Reaper{pool: pool} }

// Reap transitions every active fact whose valid_to has passed to `expired`,
// emitting one audit event each, all in a single transaction. It also reports
// how many active facts are expiring within 24h (for the facts_expiring_24h
// metric). It is idempotent: a fact already expired is not touched again.
func (r *Reaper) Reap(ctx context.Context) (Result, error) {
	var res Result
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		// Select the soon-to-be-expired ids first so we can audit each one.
		rows, err := tx.Query(ctx,
			`SELECT id FROM facts WHERE status = 'active' AND valid_to IS NOT NULL AND valid_to <= now()`)
		if err != nil {
			return fmt.Errorf("reaper: select expired: %w", err)
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, id := range ids {
			if _, err := tx.Exec(ctx,
				`UPDATE facts SET status = 'expired', version = version + 1, updated_at = now() WHERE id = $1`, id); err != nil {
				return fmt.Errorf("reaper: expire %s: %w", id, err)
			}
			if err := audit.Write(ctx, tx, audit.Event{
				Principal: "system:reaper", Action: "fact.expired", SubjectType: "fact",
				SubjectID: id, FromState: "active", ToState: "expired",
			}); err != nil {
				return err
			}
		}
		res.Expired = len(ids)

		// Count facts expiring in the next 24h (metric input, not a transition).
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM facts
			WHERE status = 'active' AND valid_to IS NOT NULL
			  AND valid_to > now() AND valid_to <= now() + interval '24 hours'`).Scan(&res.ExpiringSoon); err != nil {
			return fmt.Errorf("reaper: count expiring: %w", err)
		}
		return nil
	})
	return res, err
}

func (r *Reaper) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reaper: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

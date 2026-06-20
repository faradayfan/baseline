package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is returned when a promotion lookup misses.
var ErrNotFound = errors.New("promotion not found")

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const selectCols = `
	SELECT id, fact_id, target_namespace_id, proposed_statement, state,
	       candidate_memory_ids, proposer, reviews, required_approvals,
	       conflict_with, idempotency_key, created_at, updated_at
	FROM promotion_requests`

// insertPromotion creates a promotion row.
func insertPromotion(ctx context.Context, q Querier, p PromotionRequest) (PromotionRequest, error) {
	reviewsJSON, _ := json.Marshal(orEmptyReviews(p.Reviews))
	const ins = `
		INSERT INTO promotion_requests
			(fact_id, target_namespace_id, proposed_statement, state,
			 candidate_memory_ids, proposer, reviews, required_approvals,
			 conflict_with, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at`
	row := q.QueryRow(ctx, ins,
		p.FactID, p.TargetNamespaceID, p.ProposedStatement, p.State,
		orEmptyStr(p.CandidateMemoryIDs), p.Proposer, reviewsJSON, p.RequiredApprovals,
		p.ConflictWith, p.IdempotencyKey)
	if err := row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return PromotionRequest{}, fmt.Errorf("promotions: insert: %w", err)
	}
	return p, nil
}

// getByID loads one promotion. Use forUpdate inside a tx to lock the row while
// recording a review (prevents lost updates to reviews[]).
func getByID(ctx context.Context, q Querier, id uuid.UUID, forUpdate bool) (PromotionRequest, error) {
	sql := selectCols + ` WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	return scanOne(q.QueryRow(ctx, sql, id))
}

// findByIdempotencyKey returns an existing promotion for (proposer, key), or
// ErrNotFound. Drives idempotent POST /promotions (§13).
func findByIdempotencyKey(ctx context.Context, q Querier, proposer, key string) (PromotionRequest, error) {
	return scanOne(q.QueryRow(ctx,
		selectCols+` WHERE proposer = $1 AND idempotency_key = $2`, proposer, key))
}

// list returns promotions filtered by namespace, state, and/or proposer (inbox).
func list(ctx context.Context, q Querier, ns *uuid.UUID, state *State, proposer *string) ([]PromotionRequest, error) {
	sql := selectCols + ` WHERE 1=1`
	var args []any
	if ns != nil {
		args = append(args, *ns)
		sql += fmt.Sprintf(" AND target_namespace_id = $%d", len(args))
	}
	if state != nil {
		args = append(args, *state)
		sql += fmt.Sprintf(" AND state = $%d", len(args))
	}
	if proposer != nil {
		args = append(args, *proposer)
		sql += fmt.Sprintf(" AND proposer = $%d", len(args))
	}
	sql += " ORDER BY created_at"

	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("promotions: list: %w", err)
	}
	defer rows.Close()
	var out []PromotionRequest
	for rows.Next() {
		p, err := scanRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// updateStateAndReviews persists a state change and the reviews log together.
func updateStateAndReviews(ctx context.Context, q Querier, id uuid.UUID, state State, reviews []Review) error {
	reviewsJSON, _ := json.Marshal(orEmptyReviews(reviews))
	tag, err := q.Exec(ctx, `
		UPDATE promotion_requests
		SET state = $2, reviews = $3, updated_at = now()
		WHERE id = $1`, id, state, reviewsJSON)
	if err != nil {
		return fmt.Errorf("promotions: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// updateStatement edits the proposed statement (review-time edit, §8.3).
func updateStatement(ctx context.Context, q Querier, id uuid.UUID, statement string) error {
	_, err := q.Exec(ctx,
		`UPDATE promotion_requests SET proposed_statement = $2, updated_at = now() WHERE id = $1`, id, statement)
	return err
}

// --- scanning ---

func scanOne(row pgx.Row) (PromotionRequest, error) {
	p, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PromotionRequest{}, ErrNotFound
	}
	return p, err
}

type scannable interface{ Scan(dest ...any) error }

func scanRow(row scannable) (PromotionRequest, error) {
	var (
		p           PromotionRequest
		reviewsJSON []byte
	)
	err := row.Scan(
		&p.ID, &p.FactID, &p.TargetNamespaceID, &p.ProposedStatement, &p.State,
		&p.CandidateMemoryIDs, &p.Proposer, &reviewsJSON, &p.RequiredApprovals,
		&p.ConflictWith, &p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PromotionRequest{}, err
		}
		return PromotionRequest{}, fmt.Errorf("promotions: scan: %w", err)
	}
	if len(reviewsJSON) > 0 {
		_ = json.Unmarshal(reviewsJSON, &p.Reviews)
	}
	return p, nil
}

func scanRows(rows pgx.Rows) (PromotionRequest, error) { return scanRow(rows) }

func orEmptyStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyReviews(r []Review) []Review {
	if r == nil {
		return []Review{}
	}
	return r
}

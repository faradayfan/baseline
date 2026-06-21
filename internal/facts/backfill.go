package facts

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Embedder is the minimal embedder dependency the backfill needs. embed.Client
// satisfies it; kept as an interface so this package does not import embed.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// BackfillResult reports a backfill pass.
type BackfillResult struct {
	Scanned  int // active facts with a NULL embedding found
	Embedded int // successfully embedded + stored
	Failed   int // embedder errors (left NULL; a later pass retries)
}

// BackfillEmbeddings embeds every active fact whose embedding is NULL and stores
// it. It exists because the embedder is best-effort on the write path (a fact may
// activate with a NULL embedding during an embedder outage) and because facts
// predating the embedding wiring have none. Idempotent: a re-run only touches the
// facts that are still NULL, so it self-heals transient write-path gaps.
//
// Continue-on-error: one fact failing to embed never aborts the pass — it is
// counted in Failed and left NULL for the next run. Each successful embedding is
// committed independently (per-row UPDATE), so a crash mid-pass loses no prior
// progress.
func BackfillEmbeddings(ctx context.Context, q Querier, e Embedder) (BackfillResult, error) {
	var res BackfillResult

	// Only active facts are searchable, so only they need embeddings.
	rows, err := q.Query(ctx, selectCols+` WHERE status = 'active' AND embedding IS NULL`)
	if err != nil {
		return res, fmt.Errorf("facts: backfill scan: %w", err)
	}
	type todo struct {
		id   uuid.UUID
		text string
	}
	var work []todo
	for rows.Next() {
		f, err := scanRowsFact(rows)
		if err != nil {
			rows.Close()
			return res, err
		}
		work = append(work, todo{id: f.ID, text: EmbedText(f)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	res.Scanned = len(work)

	for _, w := range work {
		vec, err := e.Embed(ctx, w.text)
		if err != nil {
			res.Failed++
			continue // leave NULL; next pass retries
		}
		if err := SetEmbedding(ctx, q, w.id, vec); err != nil {
			res.Failed++
			continue
		}
		res.Embedded++
	}
	return res, nil
}

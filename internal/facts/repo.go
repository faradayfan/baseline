package facts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// ErrNotFound is returned when a fact lookup misses.
var ErrNotFound = errors.New("fact not found")

// Repo is the store-backed fact repository. Methods accept a Querier so they can
// run either on the pool or inside a caller-supplied transaction (the promotions
// workflow drives multi-step transitions in one tx).
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Pool exposes the underlying pool for callers that need to begin a transaction.
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

// Insert creates a fact. canonical_key is ALWAYS derived from subject here — the
// single write path (§4.6, §14.17); any client-provided key is ignored.
func Insert(ctx context.Context, q Querier, f Fact) (Fact, error) {
	f.CanonicalKey = f.Subject.CanonicalKey()
	subjectJSON, _ := json.Marshal(f.Subject)
	provJSON, _ := json.Marshal(f.Provenance)
	metaJSON, _ := json.Marshal(orEmptyMap(f.Metadata))

	const ins = `
		INSERT INTO facts
			(namespace_id, statement, subject, canonical_key, status, confidence,
			 source_memory_ids, provenance, valid_from, valid_to, supersedes_id,
			 tags, metadata, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, version, created_at, updated_at`
	row := q.QueryRow(ctx, ins,
		f.NamespaceID, f.Statement, subjectJSON, f.CanonicalKey, f.Status, f.Confidence,
		orEmpty(f.SourceMemoryIDs), provJSON, f.ValidFrom, f.ValidTo, f.SupersedesID,
		orEmpty(f.Tags), metaJSON, f.CreatedBy)
	if err := row.Scan(&f.ID, &f.Version, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return Fact{}, fmt.Errorf("facts: insert: %w", err)
	}
	return f, nil
}

// SetEmbedding stores a fact's embedding vector (§11.1). Baseline owns its fact
// embeddings, decoupled from any memory backend. The vector width must equal the
// facts.embedding vector(N) column — the embedder's Dims guard enforces this
// before the vector ever reaches here. Called on the write path after activation
// (best-effort; a NULL embedding is tolerated — see promotions.activate) and by
// the backfill routine.
func SetEmbedding(ctx context.Context, q Querier, id uuid.UUID, vec []float32) error {
	_, err := q.Exec(ctx, `UPDATE facts SET embedding = $2 WHERE id = $1`,
		id, pgvector.NewVector(vec))
	if err != nil {
		return fmt.Errorf("facts: set embedding: %w", err)
	}
	return nil
}

// EmbedText renders the text fed to the embedder for a fact: the structured
// subject (its identity) followed by the free-text statement. Including the
// subject improves recall for queries phrased around what a fact is ABOUT, not
// just its prose. Deterministic — qualifiers are sorted — so re-embedding the
// same fact yields the same input. Must mirror the QUERY side (raw query text is
// embedded as-is; the asymmetry is intentional: facts carry structure, queries
// don't).
func EmbedText(f Fact) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(f.Subject.Type))
	if sc := strings.TrimSpace(f.Subject.Scope); sc != "" {
		b.WriteString(" ")
		b.WriteString(sc)
	}
	if len(f.Subject.Qualifiers) > 0 {
		quals := make([]string, 0, len(f.Subject.Qualifiers))
		for k, v := range f.Subject.Qualifiers {
			quals = append(quals, strings.TrimSpace(k)+"="+strings.TrimSpace(v))
		}
		sort.Strings(quals)
		b.WriteString(" ")
		b.WriteString(strings.Join(quals, " "))
	}
	b.WriteString("\n")
	b.WriteString(f.Statement)
	return b.String()
}

// Get returns one fact by ID.
func Get(ctx context.Context, q Querier, id uuid.UUID) (Fact, error) {
	return scanOne(q.QueryRow(ctx, selectCols+` WHERE id = $1`, id))
}

// ListFilter narrows a facts query (§9 GET /facts).
type ListFilter struct {
	Namespaces   []uuid.UUID // restrict to these (caller's entitlements)
	Status       *Status
	CanonicalKey *string
	Tag          *string   // single-tag exact membership (legacy)
	Tags         []string  // ANY-of these tags (OR); authoritative:true and tier:always always pass
	Text         *string   // q: case-insensitive substring (fallback when no QueryVec)
	QueryVec     []float32 // q embedded: rank by cosine distance (semantic search)
	Limit        int
}

// List returns facts matching the filter, newest first. The Namespaces filter is
// how the caller enforces entitlement scoping — pass the readable set.
func List(ctx context.Context, q Querier, f ListFilter) ([]Fact, error) {
	sql := selectCols + ` WHERE 1=1`
	var args []any
	add := func(clause string, val any) {
		args = append(args, val)
		sql += fmt.Sprintf(clause, len(args))
	}
	if f.Namespaces != nil {
		add(" AND namespace_id = ANY($%d)", f.Namespaces)
	}
	if f.Status != nil {
		add(" AND status = $%d", *f.Status)
	}
	if f.CanonicalKey != nil {
		add(" AND canonical_key = $%d", *f.CanonicalKey)
	}
	if f.Tag != nil {
		add(" AND $%d = ANY(tags)", *f.Tag)
	}
	if len(f.Tags) > 0 {
		// ANY-of-tags (pg array overlap), with two always-pass delivery markers:
		// authoritative:true (mandatory baseline) and tier:always (always-on
		// injection tier). Mirrors contextsvc's read-path filter.
		add(" AND (tags && $%d OR 'authoritative:true' = ANY(tags) OR 'tier:always' = ANY(tags))", f.Tags)
	}
	// Text search: semantic when an embedded query vector is supplied, else
	// substring fallback. QueryVec only changes the ORDER BY — it never adds a
	// WHERE clause, so entitlement scoping (Namespaces) and every other filter
	// still constrain the result set identically. Vector ranking can reorder but
	// never widen what the caller may see.
	if f.QueryVec != nil {
		// Rank by cosine distance (<=>) via the HNSW index. NULLS LAST keeps
		// facts that failed to embed (transient embedder outage, or pre-backfill)
		// findable, ranked behind the embedded ones, rather than dropping them.
		args = append(args, pgvector.NewVector(f.QueryVec))
		sql += fmt.Sprintf(" ORDER BY embedding <=> $%d ASC NULLS LAST", len(args))
	} else if f.Text != nil {
		add(" AND statement ILIKE '%%' || $%d || '%%'", *f.Text)
		sql += " ORDER BY created_at DESC"
	} else {
		sql += " ORDER BY created_at DESC"
	}
	if f.Limit > 0 {
		add(" LIMIT $%d", f.Limit)
	}

	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("facts: list: %w", err)
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		f, err := scanRowsFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FindActiveByKey returns the single active fact in a namespace with the given
// canonical key, or ErrNotFound. Used for conflict detection at propose time
// and precedence resolution. The partial unique index guarantees at most one.
func FindActiveByKey(ctx context.Context, q Querier, ns uuid.UUID, key string) (Fact, error) {
	return scanOne(q.QueryRow(ctx,
		selectCols+` WHERE namespace_id = $1 AND canonical_key = $2 AND status = 'active'`, ns, key))
}

// SetStatus transitions a fact's status, bumping version. The caller must have
// validated legality via CanTransition and authority via RBAC. Returns the new
// version. Optional setters (valid_from, approved_by, supersession) are applied
// by dedicated helpers below to keep this focused.
func SetStatus(ctx context.Context, q Querier, id uuid.UUID, to Status) error {
	tag, err := q.Exec(ctx,
		`UPDATE facts SET status = $2, version = version + 1, updated_at = now() WHERE id = $1`, id, to)
	if err != nil {
		return fmt.Errorf("facts: set status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Activate marks a proposed/in_review fact active, stamping valid_from=now() and
// the approver list. Used when a promotion reaches required approvals.
func Activate(ctx context.Context, q Querier, id uuid.UUID, approvedBy []string) error {
	tag, err := q.Exec(ctx, `
		UPDATE facts
		SET status = 'active', valid_from = now(), approved_by = $2,
		    version = version + 1, updated_at = now()
		WHERE id = $1`, id, orEmpty(approvedBy))
	if err != nil {
		return fmt.Errorf("facts: activate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrVersionConflict signals an optimistic-concurrency failure (stale If-Match).
var ErrVersionConflict = errors.New("version conflict")

// RevokeWithVersion transitions an active fact to revoked only if its current
// version matches expectedVersion, enforcing optimistic concurrency (§14.8).
// A mismatch returns ErrVersionConflict and writes nothing (→ HTTP 409).
func RevokeWithVersion(ctx context.Context, q Querier, id uuid.UUID, expectedVersion int) error {
	tag, err := q.Exec(ctx, `
		UPDATE facts SET status = 'revoked', version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = 'active'`, id, expectedVersion)
	if err != nil {
		return fmt.Errorf("facts: revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Distinguish "no such active fact" from "stale version" for a precise status.
		f, gerr := Get(ctx, q, id)
		if errors.Is(gerr, ErrNotFound) {
			return ErrNotFound
		}
		if gerr == nil && f.Status != StatusActive {
			return fmt.Errorf("facts: cannot revoke fact in status %s", f.Status)
		}
		return ErrVersionConflict
	}
	return nil
}

// ActivateAuto activates a fact via auto-promotion, stamping valid_from=now(),
// recording the engine as approver, and appending the `auto:true` tag (§14.12).
// approved_by holds the engine principal so the lineage is traceable.
func ActivateAuto(ctx context.Context, q Querier, id uuid.UUID) error {
	tag, err := q.Exec(ctx, `
		UPDATE facts
		SET status = 'active', valid_from = now(),
		    tags = (SELECT array_agg(DISTINCT t) FROM unnest(tags || ARRAY['auto:true']) AS t),
		    version = version + 1, updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("facts: activate-auto: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Supersede marks oldID superseded by newID and sets both lineage pointers,
// atomically (within the caller's tx). §14.7.
func Supersede(ctx context.Context, q Querier, oldID, newID uuid.UUID) error {
	if _, err := q.Exec(ctx, `
		UPDATE facts SET status = 'superseded', superseded_by_id = $2,
		    version = version + 1, updated_at = now()
		WHERE id = $1`, oldID, newID); err != nil {
		return fmt.Errorf("facts: supersede old: %w", err)
	}
	if _, err := q.Exec(ctx,
		`UPDATE facts SET supersedes_id = $2, updated_at = now() WHERE id = $1`, newID, oldID); err != nil {
		return fmt.Errorf("facts: supersede link new: %w", err)
	}
	return nil
}

// --- scanning ---

const selectCols = `
	SELECT id, namespace_id, statement, subject, canonical_key, status, confidence,
	       source_memory_ids, provenance, valid_from, valid_to, supersedes_id,
	       superseded_by_id, tags, metadata, created_by, approved_by, version,
	       created_at, updated_at
	FROM facts`

// scannableFact is satisfied by both pgx.Row and pgx.Rows.
type scannableFact interface{ Scan(dest ...any) error }

func scanOne(row pgx.Row) (Fact, error) {
	f, err := scanFact(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Fact{}, ErrNotFound
	}
	return f, err
}

func scanRowsFact(rows pgx.Rows) (Fact, error) { return scanFact(rows) }

func scanFact(row scannableFact) (Fact, error) {
	var (
		f                     Fact
		subjectJSON, provJSON []byte
		metaJSON              []byte
	)
	err := row.Scan(
		&f.ID, &f.NamespaceID, &f.Statement, &subjectJSON, &f.CanonicalKey, &f.Status, &f.Confidence,
		&f.SourceMemoryIDs, &provJSON, &f.ValidFrom, &f.ValidTo, &f.SupersedesID,
		&f.SupersededByID, &f.Tags, &metaJSON, &f.CreatedBy, &f.ApprovedBy, &f.Version,
		&f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return Fact{}, err
	}
	_ = json.Unmarshal(subjectJSON, &f.Subject)
	_ = json.Unmarshal(provJSON, &f.Provenance)
	_ = json.Unmarshal(metaJSON, &f.Metadata)
	return f, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

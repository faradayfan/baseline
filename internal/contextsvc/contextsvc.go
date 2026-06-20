// Package contextsvc implements the /context resolver (§10): the agent read path
// that returns the precedence-resolved set of active facts a caller is entitled
// to, optionally merged with their personal memories.
//
// The invariants this package must uphold (conformance §14.3/4/9):
//   - never return a fact outside the caller's entitled namespaces (§14.3);
//   - never return expired/revoked/superseded facts or facts past valid_to (§14.4);
//   - for each canonical_key, return the highest-precedence active fact, with
//     authoritative:true overriding specificity (§14.9).
package contextsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/memory"
)

// Source is "fact" or "memory" in a resolved item.
type Source string

const (
	SourceFact   Source = "fact"
	SourceMemory Source = "memory"
)

// Item is one entry in the resolved context (§10 step 6).
type Item struct {
	Source       Source         `json:"source"`
	Statement    string         `json:"statement"`
	NamespaceID  *uuid.UUID     `json:"namespace_id,omitempty"`
	CanonicalKey string         `json:"canonical_key,omitempty"`
	Confidence   *float64       `json:"confidence,omitempty"`
	ValidTo      *time.Time     `json:"valid_to,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// Service resolves context. It depends on the pool for facts and the memory port
// for optional personal-memory merge.
type Service struct {
	pool *pgxpool.Pool
	mem  memory.Source
}

func NewService(pool *pgxpool.Pool, mem memory.Source) *Service {
	return &Service{pool: pool, mem: mem}
}

// Query parameters for Resolve.
type Query struct {
	ActorID         string
	Namespaces      []uuid.UUID // the caller's ENTITLED namespaces (already authorized)
	IncludeMemories bool
	Limit           int

	// Tags optionally narrows facts to those carrying ANY of these tags (OR). An
	// empty set means no filtering (all entitled facts). Facts tagged
	// `authoritative:true` ALWAYS pass regardless of Tags — a mandatory baseline
	// can't be filtered out. Tags are opaque strings; Baseline ascribes no meaning.
	Tags []string
}

// kindRank orders namespace kinds by specificity (§6): user ▸ project ▸ team ▸ org.
// Higher rank = more specific = wins.
var kindRank = map[string]int{
	"user":    3,
	"project": 2,
	"team":    1,
	"org":     0,
}

// Resolve runs the §10 algorithm. The caller passes the already-computed entitled
// namespace set; this method never widens it, guaranteeing §14.3.
func (s *Service) Resolve(ctx context.Context, q Query) ([]Item, error) {
	if len(q.Namespaces) == 0 {
		// No entitlements → no facts. Still allow memory merge below.
		return s.maybeMergeMemories(ctx, q, nil)
	}

	// Active, non-expired facts in the entitled namespaces only. Filtering by
	// status='active' and valid_to here enforces §14.4 at the query level; the
	// namespace IN (...) clause enforces §14.3.
	sql := `
		SELECT f.canonical_key, f.statement, f.namespace_id, n.kind, f.confidence,
		       f.valid_to, f.tags, f.metadata
		FROM facts f
		JOIN namespaces n ON n.id = f.namespace_id
		WHERE f.status = 'active'
		  AND f.namespace_id = ANY($1)
		  AND (f.valid_to IS NULL OR f.valid_to > now())`
	args := []any{q.Namespaces}

	// Optional tag filter: keep facts whose tags overlap the requested set (pg
	// array overlap `&&`), OR that always pass the read-path filter — two
	// independent always-pass markers:
	//   - authoritative:true — a mandatory baseline (also wins precedence, §14.9);
	//   - tier:always        — the always-on DELIVERY tier (injected every session),
	//                          orthogonal to precedence; see plugin tiered-injection.
	// Both are delivery bypasses here; only authoritative also affects precedence
	// (below). Keeping them separate keeps "what it is" and "when it's injected" apart.
	if len(q.Tags) > 0 {
		args = append(args, q.Tags)
		sql += fmt.Sprintf(` AND (f.tags && $%d OR 'authoritative:true' = ANY(f.tags) OR 'tier:always' = ANY(f.tags))`, len(args))
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("contextsvc: query facts: %w", err)
	}
	defer rows.Close()

	// Group by canonical_key, keeping the highest-precedence candidate.
	type cand struct {
		item          Item
		kind          string
		authoritative bool
	}
	best := map[string]cand{}
	for rows.Next() {
		var (
			key, statement, kind string
			nsID                 uuid.UUID
			confidence           *float64
			validTo              *time.Time
			tags                 []string
			metaJSON             []byte
		)
		if err := rows.Scan(&key, &statement, &nsID, &kind, &confidence, &validTo, &tags, &metaJSON); err != nil {
			return nil, fmt.Errorf("contextsvc: scan: %w", err)
		}
		var meta map[string]any
		_ = json.Unmarshal(metaJSON, &meta)

		nsCopy := nsID
		c := cand{
			item: Item{
				Source: SourceFact, Statement: statement, NamespaceID: &nsCopy,
				CanonicalKey: key, Confidence: confidence, ValidTo: validTo, Tags: tags, Metadata: meta,
			},
			kind:          kind,
			authoritative: hasTag(tags, "authoritative:true"),
		}
		if cur, ok := best[key]; !ok || outranks(c.authoritative, c.kind, cur.authoritative, cur.kind) {
			best[key] = c
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	facts := make([]Item, 0, len(best))
	for _, c := range best {
		facts = append(facts, c.item)
	}
	// Stable order: facts by canonical_key for deterministic output.
	sort.Slice(facts, func(i, j int) bool { return facts[i].CanonicalKey < facts[j].CanonicalKey })

	return s.maybeMergeMemories(ctx, q, facts)
}

// outranks reports whether candidate A beats candidate B for the same key:
// authoritative wins first (§14.9), then namespace specificity (§6).
func outranks(aAuth bool, aKind string, bAuth bool, bKind string) bool {
	if aAuth != bAuth {
		return aAuth // authoritative beats non-authoritative
	}
	return kindRank[aKind] > kindRank[bKind]
}

// maybeMergeMemories appends the actor's personal memories below all facts
// (lowest precedence, §10 step 5), de-duplicated against facts by canonical_key
// where the memory carries one in metadata. Memories never override a fact.
func (s *Service) maybeMergeMemories(ctx context.Context, q Query, facts []Item) ([]Item, error) {
	out := facts
	if !q.IncludeMemories || s.mem == nil {
		return applyLimit(out, q.Limit), nil
	}

	mems, err := s.mem.List(ctx, q.ActorID, memory.ListOpts{Limit: q.Limit})
	if err != nil {
		return nil, fmt.Errorf("contextsvc: list memories: %w", err)
	}
	factKeys := map[string]struct{}{}
	for _, f := range facts {
		factKeys[f.CanonicalKey] = struct{}{}
	}
	for _, m := range mems {
		// If the memory declares a canonical_key already covered by a fact, drop it.
		if k, ok := m.Metadata["canonical_key"].(string); ok {
			if _, covered := factKeys[k]; covered {
				continue
			}
		}
		out = append(out, Item{
			Source: SourceMemory, Statement: m.Content, Metadata: m.Metadata,
		})
	}
	return applyLimit(out, q.Limit), nil
}

func applyLimit(items []Item, limit int) []Item {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

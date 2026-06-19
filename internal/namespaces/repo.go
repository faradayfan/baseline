package namespaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a namespace lookup misses.
var ErrNotFound = errors.New("namespace not found")

// Repo is the store-backed namespace registry.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Create inserts a namespace. If ns.Policy is the zero value, the kind's default
// policy (§7.3) is applied.
func (r *Repo) Create(ctx context.Context, ns Namespace) (Namespace, error) {
	if ns.Policy.isZero() {
		ns.Policy = DefaultPolicy(ns.Kind)
	}
	policyJSON, err := marshalPolicy(ns.Policy)
	if err != nil {
		return Namespace{}, err
	}

	const q = `
		INSERT INTO namespaces (name, kind, parent_id, policy)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`
	row := r.pool.QueryRow(ctx, q, ns.Name, ns.Kind, ns.ParentID, policyJSON)
	if err := row.Scan(&ns.ID, &ns.CreatedAt); err != nil {
		return Namespace{}, fmt.Errorf("namespaces: create: %w", err)
	}
	return ns, nil
}

// Get returns one namespace by ID.
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (Namespace, error) {
	const q = `SELECT id, name, kind, parent_id, policy, created_at FROM namespaces WHERE id = $1`
	return scanOne(r.pool.QueryRow(ctx, q, id))
}

// List returns all namespaces ordered by name.
func (r *Repo) List(ctx context.Context) ([]Namespace, error) {
	const q = `SELECT id, name, kind, parent_id, policy, created_at FROM namespaces ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("namespaces: list: %w", err)
	}
	defer rows.Close()

	var out []Namespace
	for rows.Next() {
		ns, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ns)
	}
	return out, rows.Err()
}

// UpdatePolicy replaces a namespace's policy. The caller is responsible for
// having validated any auto-promote rules against the pinned engine first (§7.4).
func (r *Repo) UpdatePolicy(ctx context.Context, id uuid.UUID, p Policy) error {
	policyJSON, err := marshalPolicy(p)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `UPDATE namespaces SET policy = $2 WHERE id = $1`, id, policyJSON)
	if err != nil {
		return fmt.Errorf("namespaces: update policy: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- scanning / json helpers ---

type scannable interface {
	Scan(dest ...any) error
}

func scanOne(row pgx.Row) (Namespace, error) {
	ns, err := scanRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Namespace{}, ErrNotFound
	}
	return ns, err
}

func scanRow(row scannable) (Namespace, error) {
	var (
		ns         Namespace
		policyJSON []byte
	)
	if err := row.Scan(&ns.ID, &ns.Name, &ns.Kind, &ns.ParentID, &policyJSON, &ns.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Namespace{}, err
		}
		return Namespace{}, fmt.Errorf("namespaces: scan: %w", err)
	}
	if err := unmarshalPolicy(policyJSON, &ns.Policy); err != nil {
		return Namespace{}, err
	}
	return ns, nil
}

// policyWire mirrors Policy but renders AutoPromote.Rules as raw JSON rather than
// a base64 []byte, so the jsonb column stays human-readable and queryable.
type policyWire struct {
	RequiredApprovals int      `json:"required_approvals"`
	AllowedProposers  []string `json:"allowed_proposers,omitempty"`
	AutoPromote       *struct {
		Engine string          `json:"engine"`
		Rules  json.RawMessage `json:"rules,omitempty"`
	} `json:"auto_promote,omitempty"`
}

func marshalPolicy(p Policy) ([]byte, error) {
	w := policyWire{RequiredApprovals: p.RequiredApprovals, AllowedProposers: p.AllowedProposers}
	if p.AutoPromote != nil {
		w.AutoPromote = &struct {
			Engine string          `json:"engine"`
			Rules  json.RawMessage `json:"rules,omitempty"`
		}{Engine: p.AutoPromote.Engine, Rules: json.RawMessage(p.AutoPromote.Rules)}
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("namespaces: marshal policy: %w", err)
	}
	return b, nil
}

func unmarshalPolicy(b []byte, p *Policy) error {
	if len(b) == 0 {
		return nil
	}
	var w policyWire
	if err := json.Unmarshal(b, &w); err != nil {
		return fmt.Errorf("namespaces: unmarshal policy: %w", err)
	}
	p.RequiredApprovals = w.RequiredApprovals
	p.AllowedProposers = w.AllowedProposers
	if w.AutoPromote != nil {
		p.AutoPromote = &AutoPromote{Engine: w.AutoPromote.Engine, Rules: []byte(w.AutoPromote.Rules)}
	}
	return nil
}

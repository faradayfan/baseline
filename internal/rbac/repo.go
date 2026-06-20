package rbac

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidRole is returned when an unknown role is granted.
var ErrInvalidRole = errors.New("invalid role")

// Repo is the store-backed membership registry and entitlement resolver.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Grant adds a role for a principal in a namespace (idempotent on the unique
// (principal, namespace_id, role) constraint).
func (r *Repo) Grant(ctx context.Context, principal string, ns uuid.UUID, role Role) error {
	if !ValidRole(role) {
		return fmt.Errorf("%w: %q", ErrInvalidRole, role)
	}
	const q = `
		INSERT INTO memberships (principal, namespace_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (principal, namespace_id, role) DO NOTHING`
	if _, err := r.pool.Exec(ctx, q, principal, ns, string(role)); err != nil {
		return fmt.Errorf("rbac: grant: %w", err)
	}
	return nil
}

// Revoke removes a role grant. No error if it didn't exist.
func (r *Repo) Revoke(ctx context.Context, principal string, ns uuid.UUID, role Role) error {
	const q = `DELETE FROM memberships WHERE principal = $1 AND namespace_id = $2 AND role = $3`
	if _, err := r.pool.Exec(ctx, q, principal, ns, string(role)); err != nil {
		return fmt.Errorf("rbac: revoke: %w", err)
	}
	return nil
}

// ListMembers returns all grants in a namespace (direct only).
func (r *Repo) ListMembers(ctx context.Context, ns uuid.UUID) ([]Grant, []string, error) {
	const q = `SELECT principal, role FROM memberships WHERE namespace_id = $1 ORDER BY principal, role`
	rows, err := r.pool.Query(ctx, q, ns)
	if err != nil {
		return nil, nil, fmt.Errorf("rbac: list members: %w", err)
	}
	defer rows.Close()

	var grants []Grant
	var principals []string
	for rows.Next() {
		var p, role string
		if err := rows.Scan(&p, &role); err != nil {
			return nil, nil, fmt.Errorf("rbac: scan member: %w", err)
		}
		grants = append(grants, Grant{NamespaceID: ns, Role: Role(role)})
		principals = append(principals, p)
	}
	return grants, principals, rows.Err()
}

// Resolve builds a principal's full Entitlements:
//   - direct roles per namespace (govern write-ish actions), and
//   - the readable set: every directly-granted namespace plus all its ancestors
//     reached up the parent chain (read inheritance, §6).
//
// A single recursive CTE seeds from the principal's granted namespaces and walks
// parent_id upward, so org becomes readable to a team:platform-eng member.
func (r *Repo) Resolve(ctx context.Context, p Principal) (Entitlements, error) {
	ent := Entitlements{
		Principal: p,
		direct:    map[uuid.UUID][]Role{},
		readable:  map[uuid.UUID]struct{}{},
	}

	// Direct grants.
	const grantsQ = `SELECT namespace_id, role FROM memberships WHERE principal = $1`
	rows, err := r.pool.Query(ctx, grantsQ, p.ID)
	if err != nil {
		return Entitlements{}, fmt.Errorf("rbac: resolve grants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nsID uuid.UUID
		var role string
		if err := rows.Scan(&nsID, &role); err != nil {
			return Entitlements{}, fmt.Errorf("rbac: scan grant: %w", err)
		}
		ent.direct[nsID] = append(ent.direct[nsID], Role(role))
	}
	if err := rows.Err(); err != nil {
		return Entitlements{}, err
	}

	// Readable set = granted namespaces ∪ their ancestors (parent chain).
	const readableQ = `
		WITH RECURSIVE granted AS (
			SELECT DISTINCT namespace_id AS id FROM memberships WHERE principal = $1
		),
		ancestors AS (
			SELECT n.id, n.parent_id FROM namespaces n JOIN granted g ON n.id = g.id
			UNION
			SELECT n.id, n.parent_id FROM namespaces n JOIN ancestors a ON n.id = a.parent_id
		)
		SELECT id FROM ancestors`
	arows, err := r.pool.Query(ctx, readableQ, p.ID)
	if err != nil {
		return Entitlements{}, fmt.Errorf("rbac: resolve readable: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var id uuid.UUID
		if err := arows.Scan(&id); err != nil {
			return Entitlements{}, fmt.Errorf("rbac: scan readable: %w", err)
		}
		ent.readable[id] = struct{}{}
	}
	return ent, arows.Err()
}

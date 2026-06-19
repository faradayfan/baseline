// Package namespaces is the registry of owning scopes and their governance
// policy (spec §4.3, §6, §7.3). Facts belong to exactly one namespace; reads
// resolve transitively up the parent chain (handled in rbac/contextsvc).
package namespaces

import (
	"time"

	"github.com/google/uuid"
)

// Kind is the namespace category. Precedence for read resolution is
// user ▸ project ▸ team ▸ org (§6), handled by the context resolver.
type Kind string

const (
	KindUser    Kind = "user"
	KindTeam    Kind = "team"
	KindProject Kind = "project"
	KindOrg     Kind = "org"
)

// Namespace is an owning scope for facts.
type Namespace struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Kind      Kind       `json:"kind"`
	ParentID  *uuid.UUID `json:"parent_id,omitempty"`
	Policy    Policy     `json:"policy"`
	CreatedAt time.Time  `json:"created_at"`
}

// Policy is the per-namespace governance configuration (§7.3). It is stored as
// jsonb. AutoPromote is optional: nil means every promotion goes to human review.
type Policy struct {
	RequiredApprovals int          `json:"required_approvals"`
	AllowedProposers  []string     `json:"allowed_proposers,omitempty"`
	AutoPromote       *AutoPromote `json:"auto_promote,omitempty"`
}

// isZero reports whether the policy is the empty value, used to apply a kind's
// default when none is supplied at create time.
func (p Policy) isZero() bool {
	return p.RequiredApprovals == 0 && p.AllowedProposers == nil && p.AutoPromote == nil
}

// AutoPromote names a versioned engine (family/vN) and its engine-specific rules.
// The rules schema is opaque here — it is validated by the pinned engine at
// policy-write time (§7.4). Stored as raw JSON so a namespace can never hold
// rules its engine cannot interpret once Validate passes.
type AutoPromote struct {
	Engine string          `json:"engine"`           // e.g. "simple/v1"
	Rules  []byte          `json:"rules,omitempty"`  // raw JSON, engine-specific
}

// DefaultPolicy returns the seeded default for a namespace kind (§7.3):
//   org     → 2 approvals, no auto-promote
//   team    → 1 approval, simple/v1 (rules empty until configured)
//   project → 1 approval, no auto-promote
//   user    → 1 approval, no auto-promote (private scope)
func DefaultPolicy(k Kind) Policy {
	switch k {
	case KindOrg:
		return Policy{RequiredApprovals: 2}
	case KindTeam:
		return Policy{RequiredApprovals: 1, AutoPromote: &AutoPromote{Engine: "simple/v1"}}
	default: // project, user
		return Policy{RequiredApprovals: 1}
	}
}

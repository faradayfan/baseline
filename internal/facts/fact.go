package facts

import (
	"time"

	"github.com/google/uuid"
)

// Status is a fact's lifecycle state (§5).
type Status string

const (
	StatusProposed   Status = "proposed"
	StatusInReview   Status = "in_review"
	StatusActive     Status = "active"
	StatusSuperseded Status = "superseded"
	StatusRevoked    Status = "revoked"
	StatusExpired    Status = "expired"
	StatusRejected   Status = "rejected"
)

// Provenance records where a fact came from (§4.1).
type Provenance struct {
	OriginActor   string   `json:"origin_actor,omitempty"`
	OriginSession string   `json:"origin_session,omitempty"`
	DerivedFrom   []string `json:"derived_from,omitempty"`
	Rationale     string   `json:"rationale,omitempty"`
}

// Fact is a curated statement promoted into a namespace (§4.1). The source of
// truth lives in Baseline's own DB.
type Fact struct {
	ID              uuid.UUID      `json:"id"`
	NamespaceID     uuid.UUID      `json:"namespace_id"`
	Statement       string         `json:"statement"`
	Subject         Subject        `json:"subject"`
	CanonicalKey    string         `json:"canonical_key"` // derived; never client-set
	Status          Status         `json:"status"`
	Confidence      *float64       `json:"confidence,omitempty"`
	SourceMemoryIDs []string       `json:"source_memory_ids,omitempty"`
	Provenance      Provenance     `json:"provenance"`
	ValidFrom       *time.Time     `json:"valid_from,omitempty"`
	ValidTo         *time.Time     `json:"valid_to,omitempty"`
	SupersedesID    *uuid.UUID     `json:"supersedes_id,omitempty"`
	SupersededByID  *uuid.UUID     `json:"superseded_by_id,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedBy       string         `json:"created_by"`
	ApprovedBy      []string       `json:"approved_by,omitempty"`
	Version         int            `json:"version"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

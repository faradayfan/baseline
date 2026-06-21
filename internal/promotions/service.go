package promotions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faradayfan/baseline/internal/audit"
	"github.com/faradayfan/baseline/internal/autopromote"
	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/namespaces"
)

// Common workflow errors surfaced to handlers (mapped to HTTP status there).
var (
	ErrNotProposer    = errors.New("only the proposer may perform this action")
	ErrInvalidState   = errors.New("promotion is not in a state that allows this action")
	ErrSelfApproval   = errors.New("proposer cannot approve their own promotion") // §14.6
	ErrInvalidSubject = errors.New("subject is required and must have a type")
)

// Service orchestrates the promotion workflow across the facts, audit, and
// namespaces packages, each step in a single transaction so a transition and
// its audit event commit atomically (§14.5).
type Service struct {
	pool     *pgxpool.Pool
	ns       *namespaces.Repo
	engines  *autopromote.Registry
	latency  LatencyRecorder
	embedder Embedder
}

// LatencyRecorder records the propose→active duration (approval_latency_seconds).
// The metrics package satisfies it; kept as an interface so this package does not
// import metrics/OTEL.
type LatencyRecorder interface {
	RecordApprovalLatency(ctx context.Context, seconds float64)
}

// Embedder produces a fact's embedding vector. The embed.Client satisfies it;
// kept as an interface so this package does not import embed (and so tests can
// inject a deterministic stub). May be nil — embedding is best-effort and the
// governance workflow never blocks on it (see embedActivatedFact).
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// embedTimeout bounds the embedder call made inside the activation transaction so
// a slow/hung Ollama can never stall an approval indefinitely — on timeout the
// fact still activates with a NULL embedding (fail-open for SEARCH; governance
// correctness is unaffected and the backfill will fill it in later).
const embedTimeout = 5 * time.Second

// NewService wires the workflow. engines may be nil (no auto-promotion anywhere);
// when non-nil, namespaces whose policy names a registered engine are evaluated
// at propose time (§7.4).
func NewService(pool *pgxpool.Pool, ns *namespaces.Repo, engines *autopromote.Registry) *Service {
	return &Service{pool: pool, ns: ns, engines: engines}
}

// WithLatencyRecorder attaches a latency recorder and returns the service, for
// fluent wiring at startup.
func (s *Service) WithLatencyRecorder(r LatencyRecorder) *Service {
	s.latency = r
	return s
}

// WithEmbedder attaches the fact embedder and returns the service, for fluent
// wiring at startup. nil → facts activate without embeddings (standards-only /
// no-Ollama deployments), search falls back to substring.
func (s *Service) WithEmbedder(e Embedder) *Service {
	s.embedder = e
	return s
}

// embedActivatedFact computes and stores a freshly-activated fact's embedding,
// best-effort. ANY failure (no embedder configured, embedder down, timeout,
// dimension mismatch) leaves embedding = NULL and returns nil: an external
// embedder outage must never fail a governance transition. The fact is searchable
// by substring meanwhile, and the backfill routine fills the NULL in later.
//
// Runs inside the caller's activation tx so the embedding commits atomically with
// the activation — but under its own short timeout so it can't hold the row lock.
func (s *Service) embedActivatedFact(ctx context.Context, tx pgx.Tx, factID uuid.UUID, f facts.Fact) {
	if s.embedder == nil {
		return
	}
	ectx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()
	vec, err := s.embedder.Embed(ectx, facts.EmbedText(f))
	if err != nil {
		return // degrade: activate with NULL embedding, backfill later
	}
	_ = facts.SetEmbedding(ctx, tx, factID, vec)
}

// ProposeInput is the propose-time payload (§8.2).
type ProposeInput struct {
	TargetNamespaceID  uuid.UUID
	ProposedStatement  string
	Subject            facts.Subject
	CandidateMemoryIDs []string
	Provenance         facts.Provenance
	Tags               []string
	Metadata           map[string]any
	Proposer           string
	IdempotencyKey     string // optional

	// Candidate signals consumed by the auto-promote engine (§7.4). Optional —
	// absent values simply won't match engine rules that reference them.
	OriginType string // provenance.origin_type
	SourceKind string // provenance.source.kind
	ActorType  string // actor.type
}

// Propose creates a Fact in `proposed` and a PromotionRequest in `pending`,
// deriving the canonical key from the subject, snapshotting required_approvals
// from namespace policy, and attaching any active-fact conflict (§8.2).
//
// Idempotent on (proposer, idempotency_key): a repeat returns the existing
// promotion rather than creating a duplicate.
func (s *Service) Propose(ctx context.Context, in ProposeInput) (PromotionRequest, error) {
	if !in.Subject.Valid() {
		return PromotionRequest{}, ErrInvalidSubject
	}

	// Idempotency short-circuit (outside the tx is fine; the unique index is the
	// real guard against races).
	if in.IdempotencyKey != "" {
		if existing, err := findByIdempotencyKey(ctx, s.pool, in.Proposer, in.IdempotencyKey); err == nil {
			return existing, nil
		} else if !errors.Is(err, ErrNotFound) {
			return PromotionRequest{}, err
		}
	}

	policy, err := s.ns.Get(ctx, in.TargetNamespaceID)
	if err != nil {
		return PromotionRequest{}, fmt.Errorf("propose: load namespace: %w", err)
	}
	required := policy.Policy.RequiredApprovals
	if required < 1 {
		required = 1
	}

	key := in.Subject.CanonicalKey()

	var out PromotionRequest
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// Conflict detection: an existing active fact with the same key (§10).
		var conflictWith *uuid.UUID
		if existing, err := facts.FindActiveByKey(ctx, tx, in.TargetNamespaceID, key); err == nil {
			id := existing.ID
			conflictWith = &id
		} else if !errors.Is(err, facts.ErrNotFound) {
			return err
		}

		fact, err := facts.Insert(ctx, tx, facts.Fact{
			NamespaceID:     in.TargetNamespaceID,
			Statement:       in.ProposedStatement,
			Subject:         in.Subject,
			Status:          facts.StatusProposed,
			SourceMemoryIDs: in.CandidateMemoryIDs,
			Provenance:      in.Provenance,
			Tags:            in.Tags,
			Metadata:        in.Metadata,
			CreatedBy:       in.Proposer,
		})
		if err != nil {
			return err
		}

		var idemKey *string
		if in.IdempotencyKey != "" {
			idemKey = &in.IdempotencyKey
		}
		out, err = insertPromotion(ctx, tx, PromotionRequest{
			FactID:             fact.ID,
			TargetNamespaceID:  in.TargetNamespaceID,
			ProposedStatement:  in.ProposedStatement,
			State:              StatePending,
			CandidateMemoryIDs: in.CandidateMemoryIDs,
			Proposer:           in.Proposer,
			RequiredApprovals:  required,
			ConflictWith:       conflictWith,
			IdempotencyKey:     idemKey,
		})
		if err != nil {
			return err
		}

		if err := audit.Write(ctx, tx, audit.Event{
			Principal:   in.Proposer,
			Action:      "fact.proposed",
			SubjectType: "promotion",
			SubjectID:   out.ID,
			ToState:     string(StatePending),
			Detail:      map[string]any{"fact_id": fact.ID, "canonical_key": key, "conflict_with": conflictWith},
		}); err != nil {
			return err
		}

		// Auto-promotion (§7.4): if the namespace pins an engine and it returns a
		// positive decision, activate straight away within this tx. Any error or
		// non-match falls through to human review — FAIL CLOSED (§14.11).
		out.FactID = fact.ID
		out.ConflictWith = conflictWith
		s.maybeAutoPromote(ctx, tx, &out, policy.Policy.AutoPromote, in)
		return nil
	})
	return out, err
}

// maybeAutoPromote evaluates the pinned engine and, on a positive decision,
// activates the fact (superseding any conflict) and marks the promotion approved
// — attributing the action to engine:<ID> and tagging the fact auto:true
// (§14.12). It is best-effort and fail-closed: any engine error, an unknown
// engine, or no match leaves the promotion in `pending` for human review, and
// such errors are swallowed here (never abort the propose tx).
func (s *Service) maybeAutoPromote(ctx context.Context, tx pgx.Tx, p *PromotionRequest, ap *namespaces.AutoPromote, in ProposeInput) {
	if s.engines == nil || ap == nil || ap.Engine == "" {
		return
	}
	cand := autopromote.Candidate{
		ProvenanceOriginType: in.OriginType,
		ProvenanceSourceKind: in.SourceKind,
		ActorType:            in.ActorType,
		Tags:                 in.Tags,
		Metadata:             in.Metadata,
	}
	decision, err := s.engines.Evaluate(ctx, ap.Engine, cand, ap.Rules)
	if err != nil || !decision.AutoPromote {
		return // fail closed → human review
	}

	enginePrincipal := "engine:" + ap.Engine

	// Supersede-before-activate to honor facts_active_unique (§14.2/7).
	if p.ConflictWith != nil {
		if err := facts.Supersede(ctx, tx, *p.ConflictWith, p.FactID); err != nil {
			return
		}
		_ = audit.Write(ctx, tx, audit.Event{
			Principal: enginePrincipal, Action: "fact.superseded", SubjectType: "fact",
			SubjectID: *p.ConflictWith, FromState: string(facts.StatusActive), ToState: string(facts.StatusSuperseded),
			Detail: map[string]any{"superseded_by": p.FactID},
		})
	}
	if err := facts.ActivateAuto(ctx, tx, p.FactID); err != nil {
		return
	}
	// Best-effort embedding for semantic search (§11.1). Never blocks promotion.
	if f, err := facts.Get(ctx, tx, p.FactID); err == nil {
		s.embedActivatedFact(ctx, tx, p.FactID, f)
	}
	p.State = StateApproved
	if err := updateStateAndReviews(ctx, tx, p.ID, p.State, p.Reviews); err != nil {
		return
	}
	_ = audit.Write(ctx, tx, audit.Event{
		Principal:   enginePrincipal, // §14.12 attribution
		Action:      "fact.auto_promoted",
		SubjectType: "fact",
		SubjectID:   p.FactID,
		ToState:     string(facts.StatusActive),
		Detail:      map[string]any{"engine": ap.Engine, "matched_rule": decision.MatchedRule, "reason": decision.Reason},
	})
}

// Submit moves a pending/changes_requested promotion (and its fact) into review.
func (s *Service) Submit(ctx context.Context, id, _ uuid.UUID, principal string) (PromotionRequest, error) {
	return s.transition(ctx, id, principal, func(p *PromotionRequest) error {
		if p.State != StatePending && p.State != StateChangesRequested {
			return ErrInvalidState
		}
		if p.Proposer != principal {
			return ErrNotProposer
		}
		p.State = StateInReview
		return nil
	}, facts.StatusInReview, "promotion.submitted")
}

// Approve records reviewer's approval. When distinct non-proposer approvals reach
// required_approvals, the fact is activated; if it conflicted with an active
// fact, that prior fact is atomically superseded with two-way lineage (§14.7).
func (s *Service) Approve(ctx context.Context, id uuid.UUID, reviewer, comment string) (PromotionRequest, error) {
	var result PromotionRequest
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		p, err := getByID(ctx, tx, id, true) // lock the row
		if err != nil {
			return err
		}
		if p.State != StateInReview && p.State != StatePending {
			return ErrInvalidState
		}
		// Separation of duties (§14.6): hard gate, regardless of policy.
		if reviewer == p.Proposer {
			return ErrSelfApproval
		}

		if !p.AlreadyApprovedBy(reviewer) {
			p.Reviews = append(p.Reviews, Review{
				Reviewer: reviewer, Decision: DecisionApprove, Comment: comment, At: time.Now().UTC(),
			})
		}

		if p.HasEnoughApprovals() {
			p.State = StateApproved
			if err := s.activate(ctx, tx, &p, reviewer); err != nil {
				return err
			}
		} else {
			p.State = StateInReview
		}

		if err := updateStateAndReviews(ctx, tx, p.ID, p.State, p.Reviews); err != nil {
			return err
		}
		result = p
		return audit.Write(ctx, tx, audit.Event{
			Principal:   reviewer,
			Action:      "promotion.approved",
			SubjectType: "promotion",
			SubjectID:   p.ID,
			ToState:     string(p.State),
			Detail:      map[string]any{"approvals": p.ApprovalCount(), "required": p.RequiredApprovals},
		})
	})
	// Record propose→active latency once the activation has committed (§13).
	if err == nil && result.State == StateApproved && s.latency != nil {
		s.latency.RecordApprovalLatency(ctx, time.Since(result.CreatedAt).Seconds())
	}
	return result, err
}

// activate transitions the fact to active and supersedes any conflicting fact,
// all within the approval transaction.
func (s *Service) activate(ctx context.Context, tx pgx.Tx, p *PromotionRequest, finalApprover string) error {
	approvers := make([]string, 0, len(p.Reviews))
	seen := map[string]struct{}{}
	for _, r := range p.Reviews {
		if r.Decision == DecisionApprove && r.Reviewer != p.Proposer {
			if _, dup := seen[r.Reviewer]; !dup {
				seen[r.Reviewer] = struct{}{}
				approvers = append(approvers, r.Reviewer)
			}
		}
	}

	// Supersede the conflicting active fact FIRST, then activate the new one.
	// Ordering matters: the partial unique index (facts_active_unique) forbids
	// two active facts sharing (namespace, canonical_key), so the old fact must
	// leave `active` before the new one enters it (§14.2, §14.7).
	if p.ConflictWith != nil {
		if err := facts.Supersede(ctx, tx, *p.ConflictWith, p.FactID); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Event{
			Principal: finalApprover, Action: "fact.superseded", SubjectType: "fact",
			SubjectID: *p.ConflictWith, FromState: string(facts.StatusActive), ToState: string(facts.StatusSuperseded),
			Detail: map[string]any{"superseded_by": p.FactID},
		}); err != nil {
			return err
		}
	}
	if err := facts.Activate(ctx, tx, p.FactID, approvers); err != nil {
		return err
	}
	// Best-effort embedding for semantic search (§11.1). Never blocks activation.
	if f, err := facts.Get(ctx, tx, p.FactID); err == nil {
		s.embedActivatedFact(ctx, tx, p.FactID, f)
	}
	return audit.Write(ctx, tx, audit.Event{
		Principal: finalApprover, Action: "fact.activated", SubjectType: "fact",
		SubjectID: p.FactID, ToState: string(facts.StatusActive),
	})
}

// Reject terminates a promotion; the fact becomes rejected.
func (s *Service) Reject(ctx context.Context, id uuid.UUID, reviewer, comment string) (PromotionRequest, error) {
	return s.review(ctx, id, reviewer, DecisionReject, comment, StateRejected, facts.StatusRejected, "promotion.rejected")
}

// RequestChanges sends a promotion back to the proposer (fact → proposed).
func (s *Service) RequestChanges(ctx context.Context, id uuid.UUID, reviewer, comment, suggested string) (PromotionRequest, error) {
	p, err := s.review(ctx, id, reviewer, DecisionRequestChanges, comment, StateChangesRequested, facts.StatusProposed, "promotion.changes_requested")
	if err != nil || suggested == "" {
		return p, err
	}
	// Apply the suggested statement edit (best-effort, separate from the gate).
	_ = updateStatement(ctx, s.pool, id, suggested)
	p.ProposedStatement = suggested
	return p, nil
}

// Withdraw lets the proposer abandon their own pending promotion (§5).
func (s *Service) Withdraw(ctx context.Context, id uuid.UUID, principal string) (PromotionRequest, error) {
	return s.transition(ctx, id, principal, func(p *PromotionRequest) error {
		if p.Proposer != principal {
			return ErrNotProposer
		}
		if p.State != StatePending && p.State != StateChangesRequested {
			return ErrInvalidState
		}
		p.State = StateRejected
		return nil
	}, facts.StatusRejected, "promotion.withdrawn")
}

// Get returns a promotion by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (PromotionRequest, error) {
	return getByID(ctx, s.pool, id, false)
}

// List returns the inbox filtered by namespace/state/proposer.
func (s *Service) List(ctx context.Context, ns *uuid.UUID, state *State, proposer *string) ([]PromotionRequest, error) {
	return list(ctx, s.pool, ns, state, proposer)
}

// --- internal helpers ---

// review records a reviewer decision that terminates or bounces the promotion
// (reject / request_changes) and moves the fact to a paired status, atomically.
func (s *Service) review(ctx context.Context, id uuid.UUID, reviewer string, d Decision, comment string, to State, factTo facts.Status, action string) (PromotionRequest, error) {
	var result PromotionRequest
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		p, err := getByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if p.State != StateInReview && p.State != StatePending {
			return ErrInvalidState
		}
		p.Reviews = append(p.Reviews, Review{Reviewer: reviewer, Decision: d, Comment: comment, At: time.Now().UTC()})
		p.State = to
		if err := updateStateAndReviews(ctx, tx, p.ID, p.State, p.Reviews); err != nil {
			return err
		}
		if err := facts.SetStatus(ctx, tx, p.FactID, factTo); err != nil {
			return err
		}
		result = p
		return audit.Write(ctx, tx, audit.Event{
			Principal: reviewer, Action: action, SubjectType: "promotion", SubjectID: p.ID, ToState: string(to),
		})
	})
	return result, err
}

// transition applies a proposer-driven state change (submit/withdraw) plus the
// paired fact status, atomically with audit.
func (s *Service) transition(ctx context.Context, id uuid.UUID, principal string, mutate func(*PromotionRequest) error, factTo facts.Status, action string) (PromotionRequest, error) {
	var result PromotionRequest
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		p, err := getByID(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if err := mutate(&p); err != nil {
			return err
		}
		if err := updateStateAndReviews(ctx, tx, p.ID, p.State, p.Reviews); err != nil {
			return err
		}
		if err := facts.SetStatus(ctx, tx, p.FactID, factTo); err != nil {
			return err
		}
		result = p
		return audit.Write(ctx, tx, audit.Event{
			Principal: principal, Action: action, SubjectType: "promotion", SubjectID: p.ID, ToState: string(p.State),
		})
	})
	return result, err
}

// inTx runs fn in a transaction, committing on nil error and rolling back otherwise.
func (s *Service) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("promotions: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

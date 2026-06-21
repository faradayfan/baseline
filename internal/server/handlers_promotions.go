package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/promotions"
	"github.com/faradayfan/baseline/internal/rbac"
)

type proposeReq struct {
	TargetNamespace    uuid.UUID        `json:"target_namespace"`
	ProposedStatement  string           `json:"proposed_statement"`
	Subject            facts.Subject    `json:"subject"`
	CandidateMemoryIDs []string         `json:"candidate_memory_ids,omitempty"`
	Provenance         facts.Provenance `json:"provenance,omitempty"`
	Tags               []string         `json:"tags,omitempty"`
	Metadata           map[string]any   `json:"metadata,omitempty"`

	// Candidate signals the auto-promote engine evaluates (§7.4). Optional.
	OriginType string `json:"origin_type,omitempty"` // provenance.origin_type
	SourceKind string `json:"source_kind,omitempty"` // provenance.source.kind
	ActorType  string `json:"actor_type,omitempty"`  // actor.type
}

// createPromotion handles POST /v1/promotions (§9). Requires contributor+ in the
// target namespace. Idempotent via the Idempotency-Key header.
func (s *Server) createPromotion(w http.ResponseWriter, r *http.Request) {
	var req proposeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, ok := authorize(w, r, req.TargetNamespace, rbac.ActionPropose); !ok {
		return
	}
	p, _ := PrincipalFrom(r.Context())
	out, err := s.promos.Propose(r.Context(), promotions.ProposeInput{
		TargetNamespaceID:  req.TargetNamespace,
		ProposedStatement:  req.ProposedStatement,
		Subject:            req.Subject,
		CandidateMemoryIDs: req.CandidateMemoryIDs,
		Provenance:         req.Provenance,
		Tags:               req.Tags,
		Metadata:           req.Metadata,
		Proposer:           p.ID,
		IdempotencyKey:     r.Header.Get("Idempotency-Key"),
		OriginType:         req.OriginType,
		SourceKind:         req.SourceKind,
		ActorType:          req.ActorType,
	})
	if errors.Is(err, promotions.ErrInvalidSubject) {
		writeError(w, http.StatusBadRequest, "subject with a type is required")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "propose failed")
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// listPromotions handles GET /v1/promotions — the governance inbox (§9). Filters:
// namespace, state, proposer (proposer=me resolves to the caller).
func (s *Server) listPromotions(w http.ResponseWriter, r *http.Request) {
	ent, ok := EntitlementsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing entitlements")
		return
	}
	var nsFilter *uuid.UUID
	if v := r.URL.Query().Get("namespace"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid namespace")
			return
		}
		if !ent.CanRead(id) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		nsFilter = &id
	}
	var stateFilter *promotions.State
	if v := r.URL.Query().Get("state"); v != "" {
		st := promotions.State(v)
		stateFilter = &st
	}
	var proposerFilter *string
	if v := r.URL.Query().Get("proposer"); v != "" {
		p, _ := PrincipalFrom(r.Context())
		if v == "me" {
			v = p.ID
		}
		proposerFilter = &v
	}

	all, err := s.promos.List(r.Context(), nsFilter, stateFilter, proposerFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	// Scope to readable namespaces (no inbox leakage outside entitlements).
	out := make([]promotions.PromotionRequest, 0, len(all))
	for _, p := range all {
		if ent.CanRead(p.TargetNamespaceID) {
			out = append(out, p)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getPromotion(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	p, err := s.promos.Get(r.Context(), id)
	if errors.Is(err, promotions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	if _, ok := authorize(w, r, p.TargetNamespaceID, rbac.ActionReadFacts); !ok {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type commentReq struct {
	Comment            string `json:"comment"`
	SuggestedStatement string `json:"suggested_statement,omitempty"`
}

// reviewAction is the shared shape of approve/reject/request-changes/submit/withdraw.
func (s *Server) promotionAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		p, err := s.promos.Get(r.Context(), id)
		if errors.Is(err, promotions.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "lookup failed")
			return
		}

		// Authorize by action: reviewer+ for approve/reject/request-changes;
		// proposer-self for submit/withdraw (verified in the service).
		principal, _ := PrincipalFrom(r.Context())
		switch action {
		case "approve", "reject", "request-changes":
			if _, ok := authorize(w, r, p.TargetNamespaceID, rbac.ActionApproveReject); !ok {
				return
			}
		}

		var body commentReq
		_ = json.NewDecoder(r.Body).Decode(&body) // body optional

		var result promotions.PromotionRequest
		switch action {
		case "submit":
			result, err = s.promos.Submit(r.Context(), id, p.FactID, principal.ID)
		case "approve":
			result, err = s.promos.Approve(r.Context(), id, principal.ID, body.Comment)
		case "reject":
			result, err = s.promos.Reject(r.Context(), id, principal.ID, body.Comment)
		case "request-changes":
			result, err = s.promos.RequestChanges(r.Context(), id, principal.ID, body.Comment, body.SuggestedStatement)
		case "withdraw":
			result, err = s.promos.Withdraw(r.Context(), id, principal.ID)
		}
		if mapped, status := mapWorkflowErr(err); mapped {
			writeError(w, status, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "action failed")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// mapWorkflowErr translates known workflow errors to HTTP statuses.
func mapWorkflowErr(err error) (bool, int) {
	switch {
	case err == nil:
		return false, 0
	case errors.Is(err, promotions.ErrSelfApproval):
		return true, http.StatusForbidden // §14.6
	case errors.Is(err, promotions.ErrNotProposer):
		return true, http.StatusForbidden
	case errors.Is(err, promotions.ErrInvalidState):
		return true, http.StatusConflict
	default:
		return false, 0
	}
}

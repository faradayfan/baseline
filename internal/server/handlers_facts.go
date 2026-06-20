package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/faradayfan/baseline/internal/facts"
	"github.com/faradayfan/baseline/internal/rbac"
)

// listFacts handles GET /v1/facts (§9). Results are scoped to the caller's
// readable namespaces — a fact outside entitlements is never returned. Filters:
// namespace, status, canonical_key, tag, q (substring), limit.
func (s *Server) listFacts(w http.ResponseWriter, r *http.Request) {
	ent, ok := EntitlementsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing entitlements")
		return
	}
	q := r.URL.Query()

	// Start from the readable set; an optional namespace filter intersects it.
	scope := ent.ReadableNamespaces()
	if v := q.Get("namespace"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid namespace")
			return
		}
		if !ent.CanRead(id) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		scope = []uuid.UUID{id}
	}

	filter := facts.ListFilter{Namespaces: scope}
	if v := q.Get("status"); v != "" {
		st := facts.Status(v)
		filter.Status = &st
	}
	if v := q.Get("canonical_key"); v != "" {
		filter.CanonicalKey = &v
	}
	if v := q.Get("tag"); v != "" {
		filter.Tag = &v
	}
	if v := q.Get("q"); v != "" {
		filter.Text = &v
	}
	if v := q.Get("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}

	// A caller with no readable namespaces gets an empty list, never all facts.
	if filter.Namespaces == nil {
		filter.Namespaces = []uuid.UUID{}
	}

	out, err := facts.List(r.Context(), s.pool, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if out == nil {
		out = []facts.Fact{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getFact(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	f, err := facts.Get(r.Context(), s.pool, id)
	if errors.Is(err, facts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	if _, ok := authorize(w, r, f.NamespaceID, rbac.ActionReadFacts); !ok {
		return
	}
	w.Header().Set("ETag", strconv.Itoa(f.Version))
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) getFactHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	f, err := facts.Get(r.Context(), s.pool, id)
	if errors.Is(err, facts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	if _, ok := authorize(w, r, f.NamespaceID, rbac.ActionReadFacts); !ok {
		return
	}
	hist, err := s.factsSvc.History(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history failed")
		return
	}
	writeJSON(w, http.StatusOK, hist)
}

// revokeFact handles PATCH /v1/facts/{id} to revoke an active fact. Requires
// reviewer+ and optimistic concurrency via the If-Match header carrying the
// fact's version (§14.8): a stale version returns 409 and writes nothing.
func (s *Server) revokeFact(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	f, err := facts.Get(r.Context(), s.pool, id)
	if errors.Is(err, facts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	if _, ok := authorize(w, r, f.NamespaceID, rbac.ActionRevoke); !ok {
		return
	}

	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		writeError(w, http.StatusPreconditionRequired, "If-Match (version) required")
		return
	}
	version, err := strconv.Atoi(ifMatch)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid If-Match")
		return
	}

	p, _ := PrincipalFrom(r.Context())
	err = s.factsSvc.Revoke(r.Context(), id, version, p.ID)
	if errors.Is(err, facts.ErrVersionConflict) {
		writeError(w, http.StatusConflict, "stale version")
		return
	}
	if errors.Is(err, facts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

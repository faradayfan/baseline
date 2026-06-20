package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/faradayfan/baseline/internal/contextsvc"
)

// getContext handles GET /v1/context — the agent read path (§9, §10). It scopes
// strictly to the caller's entitled namespaces: an optional `namespaces` query
// override is INTERSECTED with entitlements, never widening them (§14.3).
func (s *Server) getContext(w http.ResponseWriter, r *http.Request) {
	ent, ok := EntitlementsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "missing entitlements")
		return
	}
	principal, _ := PrincipalFrom(r.Context())

	// Start from the full entitled (readable) set.
	readable := ent.ReadableNamespaces()

	// Optional override: intersect, so a caller can narrow but never widen.
	if raw := r.URL.Query().Get("namespaces"); raw != "" {
		want := map[uuid.UUID]struct{}{}
		for _, part := range strings.Split(raw, ",") {
			id, err := uuid.Parse(strings.TrimSpace(part))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid namespaces filter")
				return
			}
			want[id] = struct{}{}
		}
		filtered := make([]uuid.UUID, 0, len(readable))
		for _, id := range readable {
			if _, ok := want[id]; ok {
				filtered = append(filtered, id)
			}
		}
		readable = filtered
	}

	actorID := r.URL.Query().Get("actor_id")
	if actorID == "" {
		actorID = principal.ID
	}

	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}

	items, err := s.context.Resolve(r.Context(), contextsvc.Query{
		ActorID:         actorID,
		Namespaces:      readable,
		IncludeMemories: r.URL.Query().Get("include_memories") == "true",
		Limit:           limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "context resolution failed")
		return
	}
	if items == nil {
		items = []contextsvc.Item{}
	}
	writeJSON(w, http.StatusOK, items)
}

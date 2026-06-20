package server

import (
	"encoding/json"
	"net/http"

	"github.com/faradayfan/baseline/internal/memory"
)

// addMemoryReq is the out-of-band capture payload. `content` is the raw text the
// harness wants remembered; the backend (Mem0) runs its own extraction on it.
// `actor_id` is optional and defaults to the caller's principal — a memory is
// personal to an actor, and the common case is the agent remembering for itself.
type addMemoryReq struct {
	Content  string         `json:"content"`
	ActorID  string         `json:"actor_id,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// addMemory handles POST /v1/memories — the out-of-band memory-capture path.
//
// This is a deliberate, documented exception to the §11 read-only boundary: it
// is a thin pass-through to the memory backend's write API so the agent harness
// has a single Baseline URL to post to (and reuses Baseline's principal auth).
// It does NOT touch the governance read-path or the fact store, and the
// read-only memory.Source contract is untouched — the write rides a SEPARATE
// optional memory.Writer capability, type-asserted here. A source that can't
// write (the null/standards-only source) yields 501, not a crash.
func (s *Server) addMemory(w http.ResponseWriter, r *http.Request) {
	writer, ok := s.mem.(memory.Writer)
	if !ok {
		writeError(w, http.StatusNotImplemented, "memory backend is read-only (standards-only mode); no capture path")
		return
	}

	var req addMemoryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}

	actorID := req.ActorID
	if actorID == "" {
		principal, _ := PrincipalFrom(r.Context())
		actorID = principal.ID
	}
	if actorID == "" {
		writeError(w, http.StatusBadRequest, "no actor_id and no authenticated principal")
		return
	}

	mem, err := writer.Add(r.Context(), actorID, req.Content, req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadGateway, "memory backend write failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, mem)
}

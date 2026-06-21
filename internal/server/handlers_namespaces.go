package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/faradayfan/baseline/internal/namespaces"
	"github.com/faradayfan/baseline/internal/rbac"
)

func (s *Server) listNamespaces(w http.ResponseWriter, r *http.Request) {
	all, err := s.ns.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	// Scope to namespaces the caller may read (§6). Platform admins see all.
	ent, _ := EntitlementsFrom(r.Context())
	out := make([]namespaces.Namespace, 0, len(all))
	for _, n := range all {
		if ent.CanRead(n.ID) {
			out = append(out, n)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type createNamespaceReq struct {
	Name     string             `json:"name"`
	Kind     namespaces.Kind    `json:"kind"`
	ParentID *uuid.UUID         `json:"parent_id,omitempty"`
	Policy   *namespaces.Policy `json:"policy,omitempty"`
}

// createNamespace registers a namespace. Registry management is platform_admin
// only (§7.1).
func (s *Server) createNamespace(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformAdmin(w, r) {
		return
	}
	var req createNamespaceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" || !validKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "name and valid kind required")
		return
	}
	n := namespaces.Namespace{Name: req.Name, Kind: req.Kind, ParentID: req.ParentID}
	if req.Policy != nil {
		n.Policy = *req.Policy
	}
	created, err := s.ns.Create(r.Context(), n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) getNamespace(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if _, ok := authorize(w, r, id, rbac.ActionReadFacts); !ok {
		return
	}
	n, err := s.ns.Get(r.Context(), id)
	if errors.Is(err, namespaces.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// patchNamespacePolicy edits a namespace's policy — namespace_admin only (§7.2).
func (s *Server) patchNamespacePolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if _, ok := authorize(w, r, id, rbac.ActionManage); !ok {
		return
	}
	var policy namespaces.Policy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Validate any auto-promote engine + rules against the pinned engine BEFORE
	// the write: an unknown engine ID or rules the engine can't interpret make
	// the policy invalid and are rejected here (fail closed, §14.15).
	if policy.AutoPromote != nil && policy.AutoPromote.Engine != "" {
		if err := s.engines.ValidatePolicy(policy.AutoPromote.Engine, policy.AutoPromote.Rules); err != nil {
			writeError(w, http.StatusBadRequest, "invalid auto_promote policy: "+err.Error())
			return
		}
	}
	if err := s.ns.UpdatePolicy(r.Context(), id, policy); errors.Is(err, namespaces.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// --- helpers ---

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return uuid.UUID{}, false
	}
	return id, true
}

func validKind(k namespaces.Kind) bool {
	switch k {
	case namespaces.KindUser, namespaces.KindTeam, namespaces.KindProject, namespaces.KindOrg:
		return true
	}
	return false
}

package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/faradayfan/baseline/internal/rbac"
)

type memberView struct {
	Principal string    `json:"principal"`
	Role      rbac.Role `json:"role"`
}

// listMembers returns the direct grants in a namespace. Visible to anyone who
// can read the namespace.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if _, ok := authorize(w, r, id, rbac.ActionReadFacts); !ok {
		return
	}
	grants, principals, err := s.rbac.ListMembers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list members failed")
		return
	}
	out := make([]memberView, len(grants))
	for i := range grants {
		out[i] = memberView{Principal: principals[i], Role: grants[i].Role}
	}
	writeJSON(w, http.StatusOK, out)
}

type addMemberReq struct {
	Principal string    `json:"principal"`
	Role      rbac.Role `json:"role"`
}

// addMember grants a role — namespace_admin only (§7.2 "manage members").
func (s *Server) addMember(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if _, ok := authorize(w, r, id, rbac.ActionManage); !ok {
		return
	}
	var req addMemberReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Principal == "" {
		writeError(w, http.StatusBadRequest, "principal required")
		return
	}
	if err := s.rbac.Grant(r.Context(), req.Principal, id, req.Role); errors.Is(err, rbac.ErrInvalidRole) {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "grant failed")
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// removeMember revokes a role — namespace_admin only.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if _, ok := authorize(w, r, id, rbac.ActionManage); !ok {
		return
	}
	principal := chi.URLParam(r, "principal")
	role := rbac.Role(r.URL.Query().Get("role"))
	if principal == "" || role == "" {
		writeError(w, http.StatusBadRequest, "principal and ?role= required")
		return
	}
	if err := s.rbac.Revoke(r.Context(), principal, id, role); err != nil {
		writeError(w, http.StatusInternalServerError, "revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

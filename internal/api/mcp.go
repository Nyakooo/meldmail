package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/nyakooo/meldmail/internal/mcp"
)

func (s *Server) mcpRoutes(mux *http.ServeMux) {
	if s.mcp == nil {
		return
	}
	mux.Handle("/mcp", s.mcp)
	for path, handler := range map[string]http.HandlerFunc{
		"GET /api/mcp/tokens": s.listTokens, "POST /api/mcp/tokens": s.createToken, "DELETE /api/mcp/tokens/{id}": s.revokeToken,
		"GET /api/mcp/approvals/{id}": s.reviewApproval,
		"GET /api/mcp/approvals":      s.approvals, "POST /api/mcp/approvals/{id}/approve": s.approve, "POST /api/mcp/approvals/{id}/reject": s.reject,
		"GET /api/mcp/audit": s.audit,
	} {
		mux.HandleFunc(path, s.protected(handler))
	}
}
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.MCPTokens(r.Context())
	if err != nil {
		storageError(w, err)
		return
	}
	writeJSON(w, tokens)
}
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name            string `json:"name"`
		Scope           string `json:"scope"`
		RequireApproval *bool  `json:"require_approval"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Scope == "" {
		input.Scope = "readonly"
	}
	approval := true
	if input.RequireApproval != nil {
		approval = *input.RequireApproval
	}
	token, raw, err := s.mcp.CreateToken(r.Context(), input.Name, input.Scope, approval)
	if err != nil {
		mcpError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	writeJSON(w, map[string]interface{}{"token": raw, "metadata": token})
}
func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.Error(w, "invalid token ID", 400)
		return
	}
	if err := s.mcp.RevokeToken(r.Context(), id); err != nil {
		mcpError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) approvals(w http.ResponseWriter, r *http.Request) {
	views, err := s.mcp.Approvals(r.Context())
	if err != nil {
		mcpError(w, err)
		return
	}
	writeJSON(w, views)
}
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	if err := s.mcp.Approve(r.Context(), r.PathValue("id")); err != nil {
		mcpError(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "executed"})
}
func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
	if err := s.mcp.Reject(r.Context(), r.PathValue("id")); err != nil {
		mcpError(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "rejected"})
}
func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	logs, err := s.store.AuditLogs(r.Context())
	if err != nil {
		storageError(w, err)
		return
	}
	writeJSON(w, logs)
}
func mcpError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mcp.ErrInvalid):
		http.Error(w, "invalid MCP request", 400)
	case errors.Is(err, mcp.ErrForbidden):
		http.Error(w, "token permission denied", 403)
	case errors.Is(err, mcp.ErrConflict):
		http.Error(w, "action is no longer pending; refresh approval status", 409)
	case errors.Is(err, mcp.ErrCapacity):
		http.Error(w, "approval queue is full", 503)
	default:
		storageError(w, err)
	}
}

func (s *Server) reviewApproval(w http.ResponseWriter, r *http.Request) {
	started := false
	err := s.mcp.Review(r.Context(), r.PathValue("id"), func(view mcp.ActionView) error {
		started = true
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(view)
	})
	if err != nil && !started {
		mcpError(w, err)
	}
}

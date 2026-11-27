package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Server) accountRoutes(mux *http.ServeMux) {
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /api/emails/{id}":                       s.readEmail,
		"DELETE /api/emails/{id}":                    s.purgeEmail,
		"POST /api/emails/purge":                     s.purgeEmailsBatch,
		"DELETE /api/accounts/{id}/trash":            s.emptyTrash,
		"GET /api/emails/{id}/attachments/{part_id}": s.downloadAttachment,
		"PATCH /api/emails/{id}/flags":               s.changeFlags,
		"POST /api/emails/batch":                     s.changeFlagsBatch,
		"POST /api/emails/send":                      s.sendEmail,
		"GET /api/events":                            s.events,
		"GET /api/emails":                            s.listEmails,
		"GET /api/accounts":                          s.accounts, "GET /api/accounts/{id}": s.account,
		"POST /api/accounts": s.saveAccount, "PUT /api/accounts": s.saveAccount, "PUT /api/accounts/{id}": s.saveAccount,
		"DELETE /api/accounts": s.deleteAccount, "DELETE /api/accounts/{id}": s.deleteAccount,
		"POST /api/accounts/test": s.testAccount, "POST /api/accounts/{id}/sync": s.syncAccount,
		"POST /api/accounts/export": s.exportConfig, "POST /api/accounts/import": s.importConfig,
		"GET /api/accounts/{id}/proxy": s.accountProxy, "GET /api/settings/proxy/edit": s.editProxy,
		"GET /api/settings/proxy": s.getProxy, "PUT /api/settings/proxy": s.putProxy,
	} {
		mux.HandleFunc(pattern, s.protected(handler))
	}
}
func (s *Server) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		if s.mail == nil {
			http.Error(w, "mail service unavailable", 503)
			return
		}
		next(w, r)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v interface{}) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, "invalid request", 400)
		return false
	}
	return true
}
func accountID(r *http.Request) (int64, error) {
	id := r.PathValue("id")
	if id == "" {
		id = r.URL.Query().Get("id")
	}
	return strconv.ParseInt(id, 10, 64)
}
func storageError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", 404)
	} else {
		http.Error(w, "storage operation failed", 500)
	}
}
func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.store.Accounts(r.Context())
	if err != nil {
		storageError(w, err)
		return
	}
	writeJSON(w, accounts)
}
func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid account ID", 400)
		return
	}
	a, err := s.store.Account(r.Context(), id)
	if err != nil {
		storageError(w, err)
		return
	}
	writeJSON(w, a)
}
func (s *Server) saveAccount(w http.ResponseWriter, r *http.Request) {
	var input mail.AccountInput
	if !decode(w, r, &input) {
		return
	}
	var existing *storage.Account
	if r.Method == http.MethodPut {
		id, err := accountID(r)
		if err != nil {
			id = input.ID
		}
		if id < 1 {
			http.Error(w, "invalid account ID", 400)
			return
		}
		a, err := s.store.Account(r.Context(), id)
		if err != nil {
			storageError(w, err)
			return
		}
		existing = &a
	} else if input.ID != 0 {
		http.Error(w, "ID must be omitted on create", 400)
		return
	}
	a, err := s.mail.Prepare(r.Context(), input, existing)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	saved, err := s.store.SaveAccount(r.Context(), a)
	if err != nil {
		http.Error(w, "account could not be saved; email must be unique", 409)
		return
	}
	s.mail.AccountsChanged()
	if r.Method == http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
	}
	writeJSON(w, saved)
}
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid account ID", 400)
		return
	}
	if err := s.store.DeleteAccount(r.Context(), id); err != nil {
		storageError(w, err)
		return
	}
	s.mail.AccountsChanged()
	w.WriteHeader(204)
}
func (s *Server) testAccount(w http.ResponseWriter, r *http.Request) {
	var input mail.AccountInput
	if !decode(w, r, &input) {
		return
	}
	var a storage.Account
	if input.ID > 0 {
		var err error
		a, err = s.store.Account(r.Context(), input.ID)
		if err != nil {
			storageError(w, err)
			return
		}
		if input.Email != "" {
			existing := a
			a, err = s.mail.Prepare(r.Context(), input, &existing)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
	} else {
		var err error
		a, err = s.mail.Prepare(r.Context(), input, nil)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	writeJSON(w, map[string]interface{}{"diagnostics": s.mail.Test(r.Context(), a)})
}
func (s *Server) syncAccount(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid account ID", 400)
		return
	}
	count, err := s.mail.Sync(r.Context(), id)
	if err != nil {
		if errors.Is(err, syncer.ErrBusy) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, map[string]interface{}{"synced": 0, "status": "syncing"})
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			storageError(w, err)
			return
		}
		http.Error(w, "sync failed; check proxy, TLS, credentials and provider settings", 502)
		return
	}
	writeJSON(w, map[string]int{"synced": count})
}
func (s *Server) getProxy(w http.ResponseWriter, r *http.Request) {
	endpoint, err := s.mail.GlobalProxy(r.Context())
	if err != nil {
		storageError(w, err)
		return
	}
	result := map[string]interface{}{"configured": endpoint != "", "url": "", "managed": s.mail.HostProxyURL != ""}
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil {
			http.Error(w, "proxy setting is invalid", 500)
			return
		}
		result["has_credentials"] = u.User != nil
		u.User = nil
		result["url"] = u.String()
	}
	writeJSON(w, result)
}
func (s *Server) putProxy(w http.ResponseWriter, r *http.Request) {
	if s.mail.HostProxyURL != "" {
		http.Error(w, "共用代理由 .env 管理，请修改 MELDMAIL_HOST_PROXY_PORT 后重新部署。", http.StatusConflict)
		return
	}
	var input struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &input) {
		return
	}
	if err := s.mail.SetGlobalProxy(r.Context(), input.URL); err != nil {
		http.Error(w, "invalid proxy URL or settings unavailable", 400)
		return
	}
	s.mail.AccountsChanged()
	s.getProxy(w, r)
}

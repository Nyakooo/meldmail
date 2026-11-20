package api

import (
	"errors"
	"github.com/nyakooo/meldmail/internal/storage"
	"net/http"
	"strconv"
)

func (s *Server) listEmails(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	q := storage.EmailQuery{Folder: params.Get("folder"), Q: params.Get("q"), Cursor: params.Get("cursor")}
	if value := params.Get("account_id"); value != "" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id < 0 {
			http.Error(w, "invalid account_id", 400)
			return
		}
		q.AccountID = id
	}
	if value := params.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			http.Error(w, "limit must be 1-100", 400)
			return
		}
		q.Limit = limit
	}
	if value := params.Get("unread"); value != "" {
		unread, err := strconv.ParseBool(value)
		if err != nil {
			http.Error(w, "invalid unread", 400)
			return
		}
		q.Unread = unread
	}
	page, err := s.store.ListEmails(r.Context(), q)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidQuery) {
			http.Error(w, "invalid email query", 400)
		} else {
			storageError(w, err)
		}
		return
	}
	writeJSON(w, page)
}

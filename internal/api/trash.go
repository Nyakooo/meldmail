package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Server) purgeEmail(w http.ResponseWriter, r *http.Request) {
	purgeTrash(w, r, s.mail.PurgeEmail)
}

func (s *Server) emptyTrash(w http.ResponseWriter, r *http.Request) {
	purgeTrash(w, r, s.mail.EmptyTrash)
}

func purgeTrash(w http.ResponseWriter, r *http.Request, purge func(context.Context, int64) error) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid ID", http.StatusBadRequest)
		return
	}
	switch err := purge(r.Context(), id); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, storage.ErrInvalidQuery):
		http.Error(w, "只能永久删除废纸篓中的邮件。", http.StatusConflict)
	case errors.Is(err, storage.ErrActionPending), errors.Is(err, syncer.ErrBusy):
		http.Error(w, "邮箱正在处理其他操作，请稍后重试。", http.StatusConflict)
	case errors.Is(err, imapclient.ErrTargetedExpungeUnsupported):
		http.Error(w, "该邮箱服务器不支持安全的永久删除。", http.StatusNotImplemented)
	default:
		messageError(w, err)
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Server) readEmail(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid email ID", 400)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(90 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "response deadline unavailable", 500)
		return
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(r.Context(), func() {
		defer close(canceled)
		if err := controller.SetWriteDeadline(time.Now()); err != nil {
			return
		}
	})
	defer func() {
		if !stop() {
			<-canceled
		}
		if err := controller.SetWriteDeadline(time.Time{}); err != nil {
			return
		}
	}()
	started := false
	err = s.mail.ReadTo(r.Context(), id, func(content mail.EmailContent) error {
		started = true
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(content)
	})
	if err != nil {
		if started {
			panic(http.ErrAbortHandler)
		}
		messageError(w, err)
	}
}
func messageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, imapclient.ErrBodyTooLarge):
		http.Error(w, "message body exceeds 5MiB", 413)
	case errors.Is(err, imapclient.ErrMessageGone):
		http.Error(w, "message is no longer available; synchronize the mailbox", 404)
	case errors.Is(err, mail.ErrInvalidMessage):
		http.Error(w, "invalid outgoing message", 400)
	default:
		storageError(w, err)
	}
}
func (s *Server) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid email ID", 400)
		return
	}
	started := false
	err = s.mail.Download(r.Context(), id, r.PathValue("part_id"), func(part imapclient.Part, reader io.Reader) error {
		filename := path.Base(strings.ReplaceAll(part.Filename, "\\", "/"))
		if filename == "." || filename == "/" || filename == "" {
			filename = "attachment"
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		started = true
		_, err := io.Copy(w, reader)
		return err
	})
	if err != nil {
		if !started {
			messageError(w, err)
			return
		}
		panic(http.ErrAbortHandler)
	}
}
func (s *Server) sendEmail(w http.ResponseWriter, r *http.Request) {
	var input mail.SendInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid outgoing message", 400)
		return
	}
	if err := s.mail.Send(r.Context(), input); err != nil {
		if errors.Is(err, mail.ErrInvalidMessage) {
			messageError(w, err)
		} else {
			http.Error(w, "SMTP delivery failed; check connection and credentials", 502)
		}
		return
	}
	writeJSON(w, map[string]string{"status": "sent"})
}

func (s *Server) changeFlags(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid email ID", 400)
		return
	}
	var change storage.FlagChange
	if !decode(w, r, &change) {
		return
	}
	if !change.Valid() {
		http.Error(w, "invalid flag change", 400)
		return
	}
	email, err := s.mail.ChangeFlags(r.Context(), id, change)
	if err != nil {
		if errors.Is(err, storage.ErrActionPending) {
			http.Error(w, "a change is still pending for this email", 409)
		} else {
			storageError(w, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	writeJSON(w, map[string]interface{}{"status": "pending", "email": email})
}

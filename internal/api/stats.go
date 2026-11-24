package api

import "net/http"

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	summary, err := s.store.MailSummary(r.Context())
	if err != nil {
		storageError(w, err)
		return
	}
	path, err := s.store.DatabasePath(r.Context())
	if err != nil {
		storageError(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{"mail": summary, "resources": s.observer.Snapshot(path)})
}

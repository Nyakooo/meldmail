package api

import "net/http"

// Explicit editing routes reveal the saved URL to the authenticated admin only.
// Lists and ordinary settings summaries continue to omit proxy credentials.
func (s *Server) accountProxy(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil || id < 1 {
		http.Error(w, "invalid account ID", 400)
		return
	}
	account, err := s.store.Account(r.Context(), id)
	if err != nil {
		storageError(w, err)
		return
	}
	endpoint, err := s.mail.AccountProxyURL(account)
	if err != nil {
		http.Error(w, "无法读取已保存的代理地址", 500)
		return
	}
	writeJSON(w, map[string]string{"url": endpoint})
}
func (s *Server) editProxy(w http.ResponseWriter, r *http.Request) {
	endpoint, err := s.mail.GlobalProxy(r.Context())
	if err != nil {
		http.Error(w, "无法读取已保存的代理地址", 500)
		return
	}
	writeJSON(w, map[string]string{"url": endpoint})
}

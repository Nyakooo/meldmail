package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		http.Error(w, "请先登录后再修改密码", http.StatusUnauthorized)
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	key := "password-change:" + host
	now := time.Now()
	s.mu.Lock()
	for name, value := range s.attempts {
		if now.After(value.until) {
			delete(s.attempts, name)
		}
	}
	a := s.attempts[key]
	if a.count >= 5 || len(s.attempts) >= 4096 {
		s.mu.Unlock()
		http.Error(w, "尝试过于频繁，请稍后再试", http.StatusTooManyRequests)
		return
	}
	if a.count == 0 {
		a.until = now.Add(5 * time.Minute)
	}
	a.count++
	s.attempts[key] = a
	s.mu.Unlock()
	var input struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "密码请求格式不正确", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "密码请求格式不正确", http.StatusBadRequest)
		return
	}
	if input.Current == "" || len(input.Current) > 72 || input.New == "" || len(input.New) > 72 {
		http.Error(w, "请填写当前密码和新密码；每个密码不能超过 72 字节", http.StatusBadRequest)
		return
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if !s.authenticated(r) {
		http.Error(w, "登录已失效，请重新登录", http.StatusUnauthorized)
		return
	}
	hash, err := s.store.Setting(r.Context(), "admin_password")
	if err != nil {
		http.Error(w, "无法读取密码设置，请稍后重试", http.StatusInternalServerError)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Current)) != nil {
		// A mistaken current password does not expire a valid web session.
		http.Error(w, "当前密码不正确", http.StatusBadRequest)
		return
	}
	encoded, err := bcrypt.GenerateFromPassword([]byte(input.New), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "无法设置新密码，请稍后重试", http.StatusInternalServerError)
		return
	}
	if err := s.store.PutSetting(r.Context(), "admin_password", string(encoded)); err != nil {
		http.Error(w, "无法保存新密码，请稍后重试", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	clear(s.sessions)
	delete(s.attempts, key)
	delete(s.attempts, host)
	close(s.sessionChanged)
	s.sessionChanged = make(chan struct{})
	s.mu.Unlock()
	s.cookie(w, r, "", -1)
	writeJSON(w, map[string]string{"status": "password_changed"})
}

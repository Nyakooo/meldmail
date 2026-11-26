package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nyakooo/meldmail/internal/logging"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/mcp"
	"github.com/nyakooo/meldmail/internal/observe"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

//go:embed dist
var assets embed.FS

type attempt struct {
	count int
	until time.Time
}
type Server struct {
	observer       *observe.Collector
	mcp            *mcp.Server
	mail           *mail.Service
	store          *storage.Store
	trust          bool
	mu             sync.Mutex
	authMu         sync.Mutex
	configRequests chan struct{}
	sessionChanged chan struct{}
	sessions       map[[32]byte]time.Time
	attempts       map[string]attempt
}

func New(store *storage.Store, trust bool, mailService *mail.Service, logs ...*logging.Files) (http.Handler, error) {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("api assets: %w", err)
	}
	s := &Server{observer: observe.New(), mail: mailService, store: store, trust: trust, sessions: make(map[[32]byte]time.Time), attempts: make(map[string]attempt), sessionChanged: make(chan struct{}), configRequests: make(chan struct{}, 1)}
	if mailService != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server, err := mcp.New(ctx, mailService)
		if err != nil {
			return nil, err
		}
		s.mcp = server
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/auth/password", s.changePassword)
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		writeJSON(w, map[string]bool{"authenticated": true})
	})
	s.accountRoutes(mux)
	mux.HandleFunc("GET /api/stats", s.protected(s.stats))
	if len(logs) > 0 && logs[0] != nil {
		mux.HandleFunc("GET /api/settings/logs", s.protected(func(w http.ResponseWriter, r *http.Request) {
			entries, err := logs[0].Recent()
			if err != nil {
				http.Error(w, "无法读取诊断日志", http.StatusInternalServerError)
				return
			}
			writeJSON(w, entries)
		}))
	}
	s.mcpRoutes(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.Handle("/", http.FileServer(http.FS(root)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			scheme := "http"
			if r.TLS != nil || (trust && r.Header.Get("X-Forwarded-Proto") == "https") {
				scheme = "https"
			}
			if origin != "" && origin != scheme+"://"+r.Host {
				http.Error(w, "invalid origin", 403)
				return
			}
		}
		mux.ServeHTTP(w, r)
	}), nil
}
func writeJSON(w http.ResponseWriter, v interface{}) {
	// Marshal before writing so serialization failures can return a clean error.
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		return
	}
}
func (s *Server) authenticated(r *http.Request) bool {
	_, ok := s.authSession(r)
	return ok
}
func (s *Server) authSession(r *http.Request) (<-chan struct{}, bool) {
	c, err := r.Cookie("meldmail_session")
	if err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sha256.Sum256([]byte(c.Value))
	expiry, ok := s.sessions[key]
	if !ok || time.Now().After(expiry) {
		delete(s.sessions, key)
		return nil, false
	}
	return s.sessionChanged, true
}
func (s *Server) cookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "meldmail_session", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || (s.trust && r.Header.Get("X-Forwarded-Proto") == "https"), MaxAge: maxAge})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	s.mu.Lock()
	for k, a := range s.attempts {
		if now.After(a.until) {
			delete(s.attempts, k)
		}
	}
	for k, e := range s.sessions {
		if now.After(e) {
			delete(s.sessions, k)
		}
	}
	a := s.attempts[host]
	if a.count >= 5 || len(s.attempts) >= 4096 || len(s.sessions) >= 1024 {
		s.mu.Unlock()
		http.Error(w, "too many attempts", 429)
		return
	}
	if a.count == 0 {
		a.until = now.Add(5 * time.Minute)
	}
	a.count++
	s.attempts[host] = a
	s.mu.Unlock()
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	// Serialize verification and session issuance with password changes so an
	// in-flight login cannot create a fresh session using the previous hash.
	s.authMu.Lock()
	defer s.authMu.Unlock()
	hash, err := s.store.Setting(r.Context(), "admin_password")
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "请先配置 MeldMail 登录密码。", 503)
		return
	}
	if err != nil {
		http.Error(w, "storage unavailable", 500)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		http.Error(w, "invalid credentials", 401)
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		http.Error(w, "session unavailable", 500)
		return
	}
	value := hex.EncodeToString(token)
	s.mu.Lock()
	s.sessions[sha256.Sum256([]byte(value))] = now.Add(24 * time.Hour)
	delete(s.attempts, host)
	s.mu.Unlock()
	s.cookie(w, r, value, 86400)
	writeJSON(w, map[string]bool{"authenticated": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("meldmail_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, sha256.Sum256([]byte(c.Value)))
		s.mu.Unlock()
	}
	s.cookie(w, r, "", -1)
	w.WriteHeader(204)
}

package api

import (
	"context"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginSession(t *testing.T) {
	s, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InitPassword(context.Background(), string(hash)); err != nil {
		t.Fatal(err)
	}
	h, err := New(s, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Forwarded-Proto", "https")
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "MeldMail") {
		t.Fatal("missing Vue page")
	}
	if w := request("GET", "/api/auth/me", "", nil); w.Code != 401 {
		t.Fatal("unprotected me")
	}
	w := request("POST", "/api/auth/login", `{"password":"password"}`, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("no cookie")
	}
	c := cookies[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe cookie")
	}
	if w := request("GET", "/api/auth/me", "", c); w.Code != 200 {
		t.Fatal("invalid session")
	}
	if w := request("POST", "/api/auth/logout", "", c); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if w := request("GET", "/api/auth/me", "", c); w.Code != 401 {
		t.Fatal("session not revoked")
	}
	for i := 0; i < 5; i++ {
		if w := request("POST", "/api/auth/login", `{"password":"wrong"}`, nil); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	if w := request("POST", "/api/auth/login", `{"password":"wrong"}`, nil); w.Code != 429 {
		t.Fatal("rate limit absent")
	}
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin accepted")
	}
}

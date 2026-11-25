package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

func TestConfigTransferRoutes(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "transfer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitPassword(ctx, string(hash)); err != nil {
		t.Fatal(err)
	}
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/accounts/import", "/api/accounts/export"} {
		if w := request(path, `{}`, nil); w.Code != 401 {
			t.Fatal("transfer route not protected", path, w.Code)
		}
	}
	login := request("/api/auth/login", `{"password":"fixture-admin"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	saved := request("/api/accounts", `{"email":"fixture@gmail.com","provider":"gmail","password":"existing-authorization","status":"paused"}`, cookie)
	if saved.Code != 201 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	exported := request("/api/accounts/export", `{"password":"configuration passphrase"}`, cookie)
	if exported.Code != 200 || !strings.Contains(exported.Header().Get("Content-Disposition"), "attachment") || exported.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unsafe export response", exported.Code)
	}
	if strings.Contains(exported.Body.String(), "existing-authorization") || strings.Contains(exported.Body.String(), "fixture@gmail.com") {
		t.Fatal("export contains plaintext")
	}
	var bundle mail.ConfigBundle
	if err := json.Unmarshal(exported.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"password": "configuration passphrase", "bundle": bundle})
	if err != nil {
		t.Fatal(err)
	}
	imported := request("/api/accounts/import", string(body), cookie)
	if imported.Code != 200 || !strings.Contains(imported.Body.String(), `"updated":1`) {
		t.Fatal("import failed", imported.Code, imported.Body.String())
	}
	accounts, err := store.Accounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatal("import created duplicates", err)
	}
	oversized := request("/api/accounts/import", `{"password":"`+strings.Repeat("x", 1024*1024)+`"}`, cookie)
	if oversized.Code != 400 {
		t.Fatal("oversized request accepted", oversized.Code)
	}
	// Saturated configuration operations fail before decoding another request.
	server := &Server{configRequests: make(chan struct{}, 1)}
	server.configRequests <- struct{}{}
	busy := httptest.NewRecorder()
	if server.configTransferSlot(busy) || busy.Code != 429 {
		t.Fatal("unbounded config concurrency")
	}
}

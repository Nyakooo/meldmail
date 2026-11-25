package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountRoutes(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
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
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/accounts", "", nil); w.Code != 401 {
		t.Fatal("account route unauthenticated")
	}
	login := request("POST", "/api/auth/login", `{"password":"admin"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	w := request("POST", "/api/accounts", `{"email":"test@163.com","provider":"163","password":"private-secret"}`, cookie)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "credentials_enc") {
		t.Fatal("credentials exposed")
	}
	var account storage.Account
	if err := json.Unmarshal(w.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.IMAPHost != "imap.163.com" {
		t.Fatal("missing preset")
	}
	saved, err := store.Account(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.CredentialsEnc) == 0 || strings.Contains(string(saved.CredentialsEnc), "private-secret") {
		t.Fatal("credentials not encrypted")
	}
	// A manual request overlapping the worker is already being handled, not a
	// connectivity failure. No network call is made while the limiter is held.
	func() {
		release, err := service.Limiter.Acquire(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		pending := request("POST", fmt.Sprintf("/api/accounts/%d/sync", account.ID), "{}", cookie)
		if pending.Code != http.StatusAccepted || !strings.Contains(pending.Body.String(), `"status":"syncing"`) {
			t.Fatalf("overlapping sync: status %d, body %s", pending.Code, pending.Body.String())
		}
	}()
	path := fmt.Sprintf("/api/accounts/%d", account.ID)
	if w := request("GET", path, "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = request("PUT", path, `{"email":"test@163.com","provider":"163","display_name":"Updated"}`, cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	updated, err := store.Account(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Updated" {
		t.Fatal("update missing")
	}
	credentials, err := service.Credentials(updated)
	if err != nil || credentials.Password != "private-secret" {
		t.Fatal("credentials not preserved", err)
	}
	w = request("POST", "/api/accounts/test", `{"email":"bad"}`, cookie)
	if w.Code != 400 {
		t.Fatal("invalid test input accepted")
	}
	w = request("PUT", "/api/settings/proxy", `{"url":"http://user:proxy-secret@localhost:8888"}`, cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "proxy-secret") {
		t.Fatal("proxy secret exposed", w.Code)
	}
	for _, endpoint := range []string{"/api/settings/proxy/edit", path + "/proxy"} {
		if response := request("GET", endpoint, "", nil); response.Code != http.StatusUnauthorized {
			t.Fatal("proxy editing requires authentication", response.Code)
		}
	}
	w = request("GET", "/api/settings/proxy/edit", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "proxy-secret") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("saved shared proxy not available for editing", w.Code)
	}
	w = request("PUT", path, `{"email":"test@163.com","provider":"163","proxy_mode":"socks5","proxy_url":"socks5://user:account-secret@localhost:1080"}`, cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), "account-secret") {
		t.Fatal("account proxy summary leaks credentials", w.Code)
	}
	w = request("GET", path+"/proxy", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "socks5://user:account-secret@localhost:1080") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("saved account proxy not available for editing", w.Code)
	}
	if response := request("GET", "/api/accounts/999999/proxy", "", cookie); response.Code != 404 {
		t.Fatal("missing account proxy", response.Code)
	}
	if w := request("POST", "/api/accounts/999999/sync", "", cookie); w.Code != 404 {
		t.Fatal("missing account sync should return 404", w.Code)
	}
	if w := request("DELETE", path, "", cookie); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := request("GET", path, "", cookie); w.Code != 404 {
		t.Fatal("account not removed")
	}
}

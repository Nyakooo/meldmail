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

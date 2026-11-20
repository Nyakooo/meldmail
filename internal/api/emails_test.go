package api

import (
	"context"
	"encoding/json"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmailListAPI(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "list.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitPassword(ctx, string(hash)); err != nil {
		t.Fatal(err)
	}
	a, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "list@gmail.com", Provider: "gmail"}, Password: "secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMetadata(ctx, f, storage.Email{UID: 1, Subject: "verification 123456", Snippet: "你好", ToAddrs: "[]", CCAddrs: "[]", Date: 100}); err != nil {
		t.Fatal(err)
	}
	h, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"admin"}`)))
	cookie := login.Result().Cookies()[0]
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("/api/emails", nil); w.Code != 401 {
		t.Fatal("list not protected")
	}
	w := get("/api/emails?q=123456&unread=true&limit=1", cookie)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page storage.EmailPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Emails) != 1 || page.Emails[0].Subject != "verification 123456" {
		t.Fatal("API query failed")
	}
	for _, query := range []string{"limit=0", "limit=101", "account_id=-1", "unread=invalid", "cursor=invalid"} {
		if w := get("/api/emails?"+query, cookie); w.Code != 400 {
			t.Fatalf("accepted %s: %d", query, w.Code)
		}
	}
}

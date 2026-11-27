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
	"strconv"
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

func TestBatchEmailAndApprovalAPI(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "batch-api.db"))
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
	a, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "batchapi@gmail.com", Provider: "gmail"}, Password: "secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := store.SaveMetadata(ctx, inbox, storage.Email{UID: 1, Subject: "GWM 1", ToAddrs: "[]", CCAddrs: "[]", Date: 100})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := store.SaveMetadata(ctx, inbox, storage.Email{UID: 2, Subject: "GWM 2", ToAddrs: "[]", CCAddrs: "[]", Date: 101})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"admin"}`)))
	cookie := login.Result().Cookies()[0]
	post := func(path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := post("/api/emails/batch", `{"ids":[1],"archive":true}`, nil); w.Code != 401 {
		t.Fatalf("batch endpoint not protected: %d", w.Code)
	}
	if w := post("/api/emails/batch", `{"ids":[],"archive":true}`, cookie); w.Code != 400 {
		t.Fatalf("expected 400 for empty ids, got %d", w.Code)
	}
	id1 := strconv.FormatInt(e1.ID, 10)
	id2 := strconv.FormatInt(e2.ID, 10)
	payload := `{"ids":[` + id1 + `,` + id2 + `],"is_read":true,"archive":true}`
	w := post("/api/emails/batch", payload, cookie)
	if w.Code != 202 {
		t.Fatalf("expected 202 from batch flags, got %d: %s", w.Code, w.Body.String())
	}
	// Second move on same emails must return 409 conflict.
	if w := post("/api/emails/batch", `{"ids":[`+id1+`],"delete":true}`, cookie); w.Code != 409 {
		t.Fatalf("expected 409 for pending batch move, got %d: %s", w.Code, w.Body.String())
	}
	// Batch purge on non-trash emails returns 409.
	if w := post("/api/emails/purge", `{"ids":[`+id1+`]}`, cookie); w.Code != 409 {
		t.Fatalf("expected 409 purging non-trash emails, got %d: %s", w.Code, w.Body.String())
	}
	// Batch reject with empty IDs returns 400.
	if w := post("/api/mcp/approvals/reject-batch", `{"ids":[]}`, cookie); w.Code != 400 {
		t.Fatalf("expected 400 for empty approval reject batch, got %d", w.Code)
	}
}

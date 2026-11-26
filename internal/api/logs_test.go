package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyakooo/meldmail/internal/logging"
	"github.com/nyakooo/meldmail/internal/mail"
)

func TestDiagnosticLogsRequireSessionAndReturnNewestEntries(t *testing.T) {
	store, _ := passwordFixture(t)
	logs, err := logging.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logs.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := 1; i <= 105; i++ {
		if _, err := fmt.Fprintf(logs, "{\"account_id\":%d}\n", i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fmt.Fprint(logs, "{\"account_id\":"); err != nil {
		t.Fatal(err)
	}
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(store, false, service, logs)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/settings/logs", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthorized.Code)
	}

	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"original-fixture"}`)))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("login failed: %d", login.Code)
	}
	request.AddCookie(login.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d, %v", response.Code, response.Header())
	}
	var entries []struct {
		AccountID int `json:"account_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 100 {
		t.Fatalf("got %d entries, want 100", len(entries))
	}
	for i, entry := range entries {
		if entry.AccountID != 105-i {
			t.Fatalf("entry %d account ID = %d, want %d", i, entry.AccountID, 105-i)
		}
	}
}

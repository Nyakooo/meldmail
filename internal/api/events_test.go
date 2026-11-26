package api

import (
	"bufio"
	"context"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSSEAuthenticationAndStreaming(t *testing.T) {
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "sse.db"))
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
	if err := store.InitPassword(context.Background(), string(hash)); err != nil {
		t.Fatal(err)
	}
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(store, false, service)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	h.ServeHTTP(unauthorized, httptest.NewRequest("GET", "/api/events", nil))
	if unauthorized.Code != 401 {
		t.Fatal("SSE not protected")
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"password":"admin"}`)))
	cookie := login.Result().Cookies()[0]
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookie)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("incorrect streaming headers")
	}
	if err := service.Events.Publish("new_email", map[string]int{"id": 42}); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	seenType := false
	seenData := false
	seenActivity := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == `data: {"syncing":0,"prefetch":null}` {
			seenActivity = true
		}
		if line == "event: new_email" {
			seenType = true
		}
		if line == `data: {"id":42}` {
			seenData = true
		}
		if seenData {
			break
		}
	}
	if !seenActivity || !seenType || !seenData {
		t.Fatal("SSE event not delivered", scanner.Err())
	}
}

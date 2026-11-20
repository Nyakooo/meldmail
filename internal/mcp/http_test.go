package mcp

import (
	"context"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPProtocolAndPermissionBoundaries(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "protocol.db"))
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
	server, err := New(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := server.CreateToken(ctx, "readonly", "readonly", true)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	cases := []struct {
		name, method, body, accept, content, origin, version string
		want                                                 int
	}{
		{name: "valid", method: "POST", body: body, want: 200}, {name: "no stream accept", method: "POST", body: body, accept: "application/json", want: 406}, {name: "wrong content", method: "POST", body: body, content: "text/plain", want: 415}, {name: "origin", method: "POST", body: body, origin: "https://evil.example", want: 403}, {name: "unsupported version", method: "POST", body: body, version: "not-a-version", want: 400}, {name: "batch", method: "POST", body: "[" + body + "]", want: 400}, {name: "trailing message", method: "POST", body: body + body, want: 400}, {name: "no GET stream", method: "GET", want: 405}, {name: "notification", method: "POST", body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`, want: 202},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://mail.local/mcp", strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+token)
			accept := tc.accept
			if accept == "" {
				accept = "application/json, text/event-stream"
			}
			r.Header.Set("Accept", accept)
			content := tc.content
			if content == "" {
				content = "application/json"
			}
			r.Header.Set("Content-Type", content)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.version != "" {
				r.Header.Set("MCP-Protocol-Version", tc.version)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	definition, ok := findTool("mark_email_read")
	if !ok {
		t.Fatal("missing tool")
	}
	for _, raw := range []string{`{"email_id":0}`, `{"email_id":1,"is_read":null}`, `{"email_id":1,"unknown":true}`} {
		var target struct {
			EmailID int64 `json:"email_id"`
			IsRead  *bool `json:"is_read"`
		}
		if err := decodeArguments([]byte(raw), definition, &target); err == nil {
			t.Fatalf("invalid schema accepted: %s", raw)
		}
	}
}

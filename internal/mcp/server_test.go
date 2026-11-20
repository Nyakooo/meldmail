package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/nyakooo/meldmail/internal/testmail"
)

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}
func toolOutput(t *testing.T, value *sdk.CallToolResult) map[string]interface{} {
	t.Helper()
	if value.IsError || len(value.Content) == 0 {
		t.Fatalf("tool failure: %+v", value)
	}
	content, ok := value.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatal("tool did not return JSON text")
	}
	var output map[string]interface{}
	if err := json.Unmarshal([]byte(content.Text), &output); err != nil {
		t.Fatal(err)
	}
	return output
}
func TestOfficialClientNineToolsAndApproval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "mcp@example.com", "fixture")
	smtpPort, deliveries := testmail.SMTP(t, fixture.ServerTLS)
	raw := "From: Sender <sender@example.com>\r\nTo: mcp@example.com\r\nSubject: Original MCP fixture\r\nMessage-ID: <mcp-original@example.com>\r\nContent-Type: text/plain\r\n\r\nOriginal body"
	if _, err := fixture.User.Append("INBOX", strings.NewReader(raw), &imap.AppendOptions{Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	})
	service, err := mail.New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service.TLSConfig = fixture.ClientTLS
	account, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "mcp@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: smtpPort, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	account, err = store.SaveAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sync(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListEmails(ctx, storage.EmailQuery{})
	if err != nil {
		t.Fatal(err)
	}
	emailID := page.Emails[0].ID
	server, err := New(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	token, rawToken, err := server.CreateToken(ctx, "readonly", "readonly", true)
	if err != nil {
		t.Fatal(err)
	}
	if token.Hash == rawToken || strings.Contains(token.Hash, rawToken) {
		t.Fatal("raw token stored")
	}
	connect := func(raw string) *sdk.ClientSession {
		t.Helper()
		client := sdk.NewClient(&sdk.Implementation{Name: "official-sdk-acceptance", Version: "1.0"}, nil)
		session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: httpServer.URL, HTTPClient: &http.Client{Transport: bearerTransport{http.DefaultTransport, raw}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		})
		return session
	}
	readonly := connect(rawToken)
	listed, err := readonly.ListTools(ctx, nil)
	if err != nil || len(listed.Tools) != 9 {
		t.Fatal("official client cannot discover nine tools", err)
	}
	for name, args := range map[string]interface{}{"list_accounts": map[string]interface{}{}, "list_emails": map[string]interface{}{}, "search_emails": map[string]interface{}{"q": "Original"}, "get_email": map[string]interface{}{"email_id": emailID}} {
		response, err := readonly.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		toolOutput(t, response)
	}
	direct := func(rawToken, body string) *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, "POST", httpServer.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+rawToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := httpServer.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	denied := direct(rawToken, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"send_email","arguments":{}}}`)
	if denied.StatusCode != 403 {
		t.Fatal("readonly send must return HTTP 403", denied.StatusCode)
	}
	if err := denied.Body.Close(); err != nil {
		t.Fatal(err)
	}
	_, approvalToken, err := server.CreateToken(ctx, "approval", "readwrite", true)
	if err != nil {
		t.Fatal(err)
	}
	approvedClient := connect(approvalToken)
	events, unsubscribe, err := service.Events.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	queued, err := approvedClient.CallTool(ctx, &sdk.CallToolParams{Name: "send_email", Arguments: map[string]interface{}{"account_id": account.ID, "to": []string{"recipient@example.com"}, "subject": "Approval fixture", "text": "private body only in memory"}})
	if err != nil {
		t.Fatal(err)
	}
	pending := toolOutput(t, queued)
	if pending["status"] != "pending_approval" {
		t.Fatal(pending)
	}
	actionID := pending["action_id"].(string)
	if len(deliveries) != 0 {
		t.Fatal("sent before administrator approval")
	}
	foundEvent := false
eventLoop:
	for {
		select {
		case event := <-events:
			if event.Type == "mcp_approval_required" {
				foundEvent = true
			}
		default:
			break eventLoop
		}
	}
	if !foundEvent {
		t.Fatal("approval SSE missing")
	}
	action, err := store.PendingAction(ctx, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(action.PayloadJSON, "private body") {
		t.Fatal("body persisted in pending_actions")
	}
	views, err := server.Approvals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if views[0].Payload.Text != "" {
		t.Fatal("approval list includes body")
	}
	if err := server.Review(ctx, actionID, func(view ActionView) error {
		if view.Payload.Text != "private body only in memory" {
			t.Fatal("review lost body")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- server.Approve(ctx, actionID) }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("approval executed more than once")
	}
	<-deliveries
	if len(deliveries) != 0 {
		t.Fatal("duplicate approved SMTP message")
	}
	_, writerToken, err := server.CreateToken(ctx, "writer", "readwrite", false)
	if err != nil {
		t.Fatal(err)
	}
	writer := connect(writerToken)
	marked, err := writer.CallTool(ctx, &sdk.CallToolParams{Name: "mark_email_read", Arguments: map[string]interface{}{"email_id": emailID}})
	if err != nil {
		t.Fatal(err)
	}
	toolOutput(t, marked)
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reply_email", "forward_email"} {
		args := map[string]interface{}{"account_id": account.ID, "email_id": emailID, "text": "A note"}
		if name == "forward_email" {
			args["to"] = []string{"forward@example.com"}
		}
		response, err := writer.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if toolOutput(t, response)["status"] != "sent" {
			t.Fatal("writer did not send")
		}
		message, err := netmail.ReadMessage(bytes.NewReader(<-deliveries))
		if err != nil {
			t.Fatal(err)
		}
		if name == "reply_email" && message.Header.Get("In-Reply-To") != "<mcp-original@example.com>" {
			t.Fatal("MCP reply threading missing")
		}
	}
	archived, err := writer.CallTool(ctx, &sdk.CallToolParams{Name: "archive_email", Arguments: map[string]interface{}{"email_id": emailID}})
	if err != nil {
		t.Fatal(err)
	}
	toolOutput(t, archived)
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Email(ctx, emailID)
	if err != nil || updated.FolderRole != "archive" {
		t.Fatal("MCP archive failed", err)
	}
	logs, err := store.AuditLogs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) < 9 {
		t.Fatal("audit log incomplete")
	}
	for _, log := range logs {
		if strings.Contains(log.ArgsSummary, "private body") || strings.Contains(log.ArgsSummary, rawToken) {
			t.Fatal("audit leaked sensitive payload")
		}
	}
	if err := server.RevokeToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	revoked := direct(rawToken, `{"jsonrpc":"2.0","id":10,"method":"tools/list"}`)
	if revoked.StatusCode != 401 {
		t.Fatal("revoked token still works")
	}
	if err := revoked.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

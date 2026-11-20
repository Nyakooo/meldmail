package mcp

import (
	"context"
	"errors"
	"github.com/nyakooo/meldmail/internal/mail"
	"github.com/nyakooo/meldmail/internal/storage"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApprovalRejectionExpiryRevocationAndRestart(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "lifecycle.db"))
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
	account, err := service.Prepare(ctx, mail.AccountInput{Account: storage.Account{Email: "lifecycle@gmail.com", Provider: "gmail"}, Password: "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	account, err = store.SaveAccount(ctx, account)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := server.CreateToken(ctx, "approval", "readwrite", true)
	if err != nil {
		t.Fatal(err)
	}
	input := mail.SendInput{AccountID: account.ID, Action: "send", To: []string{"recipient@example.com"}, Subject: "Metadata subject", Text: "never persisted private body"}
	enqueue := func() string {
		t.Helper()
		id, err := server.enqueue(ctx, token, input)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	rejected := enqueue()
	if err := server.Reject(ctx, rejected); err != nil {
		t.Fatal(err)
	}
	if err := server.Approve(ctx, rejected); !errors.Is(err, ErrConflict) {
		t.Fatal("rejected request executed", err)
	}
	expired := enqueue()
	server.mu.Lock()
	server.pending[expired].created = time.Now().Add(-approvalTTL - time.Second)
	server.mu.Unlock()
	if _, err := server.Approvals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := server.Approve(ctx, expired); !errors.Is(err, ErrConflict) {
		t.Fatal("expired request executed", err)
	}
	revoked := enqueue()
	if err := server.RevokeToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if err := server.Approve(ctx, revoked); !errors.Is(err, ErrConflict) && !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked request executed", err)
	}
	freshToken, _, err := server.CreateToken(ctx, "fresh", "readwrite", true)
	if err != nil {
		t.Fatal(err)
	}
	token = freshToken
	lost := enqueue()
	restarted, err := New(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	action, err := store.PendingAction(ctx, lost)
	if err != nil || action.Status != "failed" {
		t.Fatal("restart left a sendable action", err)
	}
	if strings.Contains(action.PayloadJSON, input.Text) {
		t.Fatal("body persisted")
	}
	if err := restarted.Approve(ctx, lost); !errors.Is(err, ErrConflict) {
		t.Fatal("restart replayed lost payload", err)
	}
	// Three 5MiB payloads fit the 16MiB budget; a fourth is refused, not spooled.
	large := input
	large.Text = strings.Repeat("x", 5*1024*1024)
	for i := 0; i < 3; i++ {
		if _, err := restarted.enqueue(ctx, token, large); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := restarted.enqueue(ctx, token, large); !errors.Is(err, ErrCapacity) {
		t.Fatal("approval memory budget not enforced", err)
	}
	actions, err := restarted.Approvals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range actions {
		if view.Payload.Text != "" || view.Payload.HTML != "" {
			t.Fatal("collection serialized retained bodies")
		}
	}
}

package mail

import (
	"context"
	"github.com/emersion/go-imap/v2"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/nyakooo/meldmail/internal/testmail"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoverAcknowledgedMove(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "recover@example.com", "fixture")
	if _, err := fixture.User.Append("INBOX", strings.NewReader("From: sender@example.com\r\nSubject: recovery\r\nMessage-ID: <recover@example.com>\r\n\r\nbody"), &imap.AppendOptions{Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	service, err := New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service.TLSConfig = fixture.ClientTLS
	account, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "recover@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: 465, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
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
	id := page.Emails[0].ID
	fixture.FailNextSelect("Archive")
	if _, err := service.ChangeFlags(ctx, id, storage.FlagChange{Archive: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	actions, err := store.FlagActions(ctx)
	if err != nil || len(actions) != 1 {
		t.Fatal("failed move lost its checkpoint", err)
	}
	action := actions[0]
	if !action.MoveAcknowledged || action.DestinationUID == 0 || action.DestinationName != "Archive" {
		t.Fatalf("missing acknowledgement checkpoint: %+v", action)
	}
	// Some providers return the source UIDVALIDITY in COPYUID.
	action.DestinationValidity++
	fixture.EmptyHeaderSearch()

	// A UID with a mismatching identity must never be accepted after rollover.
	wrong := action
	wrong.MessageID = "different@example.com"
	wrong.NextAt = 0
	if err := store.RetryFlagAction(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := store.FlagActions(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatal("mismatching destination identity accepted", err)
	}
	action.NextAt = 0
	if err := store.RetryFlagAction(ctx, action); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	actions, err = store.FlagActions(ctx)
	if err != nil || len(actions) != 0 {
		t.Fatal("move did not recover", err)
	}
	moved, err := store.Email(ctx, id)
	if err != nil || moved.FolderRole != "archive" {
		t.Fatal("cache still references original folder", err)
	}
	body, err := service.Read(ctx, id)
	if err != nil || !strings.Contains(body.Text, "body") {
		t.Fatal("recovered message unreadable", err)
	}
	status, err := fixture.User.Status("Archive", &imap.StatusOptions{NumMessages: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.NumMessages == nil || *status.NumMessages != 1 {
		t.Fatal("MOVE retry duplicated the message")
	}
}

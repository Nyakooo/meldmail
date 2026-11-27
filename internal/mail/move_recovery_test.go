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
	// A missing destination must leave the pending move recoverable without
	// reporting a reconnect for an account that can still sync.
	if err := store.SetAccountStatus(ctx, account.ID, "active", ""); err != nil {
		t.Fatal(err)
	}
	missing := action
	missing.DestinationName = "Missing folder"
	missing.NextAt = 0
	if err := store.RetryFlagAction(ctx, missing); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := store.Account(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "active" {
		t.Fatalf("move recovery changed connected account status to %q", current.Status)
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

func TestBatchFlagAndPurgeActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "batchmail@example.com", "fixture")
	for _, subject := range []string{"OTP 1", "OTP 2", "OTP 3"} {
		if _, err := fixture.User.Append("INBOX", strings.NewReader("From: sender@example.com\r\nSubject: "+subject+"\r\nMessage-ID: <"+strings.ReplaceAll(subject, " ", "")+"@example.com>\r\n\r\nbody"), &imap.AppendOptions{Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "batch-mail.db"))
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
	account, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "batchmail@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: 465, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
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
	if err != nil || len(page.Emails) != 3 {
		t.Fatalf("expected 3 emails, got %d err=%v", len(page.Emails), err)
	}
	ids := []int64{page.Emails[0].ID, page.Emails[1].ID, page.Emails[2].ID}
	read := true
	if _, err := service.ChangeFlagsBatch(ctx, ids, storage.FlagChange{IsRead: &read}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	// Verify batch archive of 2 messages and batch delete of the remaining message, then batch purge.
	if _, err := service.ChangeFlagsBatch(ctx, ids[:2], storage.FlagChange{Archive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ChangeFlagsBatch(ctx, ids[2:], storage.FlagChange{Delete: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	archived, err := store.ListEmails(ctx, storage.EmailQuery{Folder: "archive"})
	if err != nil || archived.Total != 2 {
		t.Fatalf("expected 2 archived emails, got %+v err=%v", archived, err)
	}
	trash, err := store.ListEmails(ctx, storage.EmailQuery{Folder: "trash"})
	if err != nil || trash.Total != 1 {
		t.Fatalf("expected 1 trashed email, got %+v err=%v", trash, err)
	}
	// Restore archived emails in batch, then move all 3 to trash and purge 2 in batch.
	if _, err := service.ChangeFlagsBatch(ctx, ids[:2], storage.FlagChange{Restore: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ChangeFlagsBatch(ctx, ids[:2], storage.FlagChange{Delete: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.PurgeEmails(ctx, ids[:2]); err != nil {
		t.Fatal(err)
	}
	trash, err = store.ListEmails(ctx, storage.EmailQuery{Folder: "trash"})
	if err != nil || trash.Total != 1 {
		t.Fatalf("expected 1 remaining trash email after batch purge, got %+v err=%v", trash, err)
	}
}

package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDurableOptimisticFlagAction(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flags.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.SaveAccount(ctx, Account{Email: "flags@example.com", Provider: "custom", AuthType: "password", IMAPHost: "imap.example.com", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "smtp.example.com", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture ciphertext"), ProxyMode: "direct", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	email, err := store.SaveMetadata(ctx, folder, Email{UID: 1, Subject: "Flags", Snippet: "bounded metadata", ToAddrs: "[]", CCAddrs: "[]", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	read := true
	if _, err := store.QueueFlags(ctx, email.ID, FlagChange{IsRead: &read, Archive: true}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.SaveMetadata(ctx, folder, email)
	if err != nil || !saved.IsRead {
		t.Fatal("in-flight sync overwrote optimistic state", err)
	}
	if err := store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	actions, err := reopened.FlagActions(ctx)
	if err != nil || len(actions) != 1 {
		t.Fatal("pending flags lost on restart", err)
	}
	page, err := reopened.ListEmails(ctx, EmailQuery{})
	if err != nil || len(page.Emails) != 0 {
		t.Fatal("pending archive not hidden", err)
	}
	flags, err := reopened.FlagBatch(ctx, folder.ID, 0)
	if err != nil || len(flags) != 0 {
		t.Fatal("reconciliation may overwrite pending flags", err)
	}
	if err := reopened.FinishFlagAction(ctx, actions[0], nil, 0, true); err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Email(ctx, email.ID)
	if err != nil || restored.IsRead {
		t.Fatal("rollback did not restore flags", err)
	}
	page, err = reopened.ListEmails(ctx, EmailQuery{})
	if err != nil || len(page.Emails) != 1 {
		t.Fatal("rollback did not restore inbox", err)
	}
}

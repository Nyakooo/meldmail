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
	// Moving messages must remain visible in their destination even before IMAP confirmation.
	for _, change := range []FlagChange{{Delete: true}, {Archive: true}} {
		if _, err := reopened.QueueFlags(ctx, email.ID, change); err != nil {
			t.Fatal(err)
		}
		target := "archive"
		if change.Delete {
			target = "trash"
		}
		page, err := reopened.ListEmails(ctx, EmailQuery{Folder: target})
		if err != nil || page.Total != 1 || len(page.Emails) != 1 || page.Emails[0].PendingAction == "" || page.Emails[0].FolderRole != target {
			t.Fatal("pending move disappeared from destination", err, page)
		}
		actions, err := reopened.FlagActions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := reopened.FinishFlagAction(ctx, actions[0], nil, 0, true); err != nil {
			t.Fatal(err)
		}
	}

}

func TestRestoreFlagValidation(t *testing.T) {
	for _, change := range []FlagChange{{Restore: true}, {Archive: true}, {Delete: true}} {
		if !change.Valid() {
			t.Fatalf("valid move rejected: %+v", change)
		}
	}
	for _, change := range []FlagChange{{Restore: true, Delete: true}, {Restore: true, Archive: true}, {Archive: true, Delete: true}, {}} {
		if change.Valid() {
			t.Fatalf("invalid move accepted: %+v", change)
		}
	}
}

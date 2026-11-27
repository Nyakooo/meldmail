package storage

import (
	"context"
	"errors"
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

func TestCoalesceAndRevisionFlagActions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "coalesce.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	a, err := store.SaveAccount(ctx, Account{Email: "coalesce@example.com", Provider: "custom", AuthType: "password", IMAPHost: "imap.example.com", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "smtp.example.com", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture"), ProxyMode: "direct", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	trash, err := store.PrepareFolder(ctx, a.ID, "Trash", "trash", 2)
	if err != nil {
		t.Fatal(err)
	}
	email, err := store.SaveMetadata(ctx, inbox, Email{UID: 10, Subject: "OTP", Snippet: "123456", ToAddrs: "[]", CCAddrs: "[]", Date: 1})
	if err != nil {
		t.Fatal(err)
	}

	read := true
	if _, err := store.QueueFlags(ctx, email.ID, FlagChange{IsRead: &read}); err != nil {
		t.Fatal(err)
	}
	actions, err := store.FlagActions(ctx)
	if err != nil || len(actions) != 1 {
		t.Fatal("missing initial flag action", err)
	}
	inFlight := actions[0]
	inFlight.Attempts = 2
	inFlight.NextAt = 9999999999999
	if err := store.RetryFlagAction(ctx, inFlight); err != nil {
		t.Fatal(err)
	}

	// Coalescing Delete onto pending IsRead must succeed and reset retry backoff.
	if _, err := store.QueueFlags(ctx, email.ID, FlagChange{Delete: true}); err != nil {
		t.Fatal("delete blocked by pending read:", err)
	}
	// Once a move is queued, another move or flag change must return ErrActionPending.
	star := true
	for _, blocked := range []FlagChange{{Archive: true}, {IsStarred: &star}} {
		if _, err := store.QueueFlags(ctx, email.ID, blocked); !errors.Is(err, ErrActionPending) {
			t.Fatalf("move pending did not block %+v: %v", blocked, err)
		}
	}

	// Stale RetryFlagAction / rollback from the older revision must not overwrite the coalesced Delete.
	if err := store.RetryFlagAction(ctx, inFlight); err != nil {
		t.Fatal(err)
	}
	if pending, err := store.CompleteFlagAction(ctx, inFlight, nil, 0, true); err != nil || !pending {
		t.Fatalf("stale rollback dropped coalesced delete: pending=%v err=%v", pending, err)
	}
	actions, err = store.FlagActions(ctx)
	if err != nil || len(actions) != 1 || !actions[0].Change.Delete || actions[0].NextAt != 0 || actions[0].Attempts != 0 {
		t.Fatalf("coalesced delete corrupted by stale retry/rollback: %+v", actions)
	}

	// Stale success completion of IsRead must preserve Delete and update OldRead.
	if pending, err := store.CompleteFlagAction(ctx, inFlight, nil, 0, false); err != nil || !pending {
		t.Fatalf("stale completion dropped coalesced delete: pending=%v err=%v", pending, err)
	}
	actions, err = store.FlagActions(ctx)
	if err != nil || len(actions) != 1 || !actions[0].Change.Delete || !actions[0].OldRead || actions[0].Change.IsRead != nil {
		t.Fatalf("unexpected action state after stale read completion: %+v", actions)
	}

	// Complete the Delete move normally.
	if pending, err := store.CompleteFlagAction(ctx, actions[0], &trash, 42, false); err != nil || pending {
		t.Fatalf("delete did not finish cleanly: pending=%v err=%v", pending, err)
	}

	// Purging a trash message removes any leftover non-move pending flag action.
	unread := false
	if _, err := store.QueueFlags(ctx, email.ID, FlagChange{IsRead: &unread}); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveTrashMetadata(ctx, a.ID, "Trash", 2, 42, 0); err != nil {
		t.Fatal(err)
	}
	actions, err = store.FlagActions(ctx)
	if err != nil || len(actions) != 0 {
		t.Fatalf("leftover flag action after trash purge: %+v", actions)
	}
}

func TestQueueFlagsBatchAndTrashBatch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	a, err := store.SaveAccount(ctx, Account{Email: "batch@example.com", Provider: "custom", AuthType: "password", IMAPHost: "imap.example.com", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "smtp.example.com", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture"), ProxyMode: "direct", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := store.PrepareFolder(ctx, a.ID, "Archive", "archive", 2)
	if err != nil {
		t.Fatal(err)
	}
	trash, err := store.PrepareFolder(ctx, a.ID, "Trash", "trash", 3)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := store.SaveMetadata(ctx, inbox, Email{UID: 1, Subject: "OTP 1", Snippet: "111", ToAddrs: "[]", CCAddrs: "[]", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := store.SaveMetadata(ctx, inbox, Email{UID: 2, Subject: "OTP 2", Snippet: "222", ToAddrs: "[]", CCAddrs: "[]", Date: 2})
	if err != nil {
		t.Fatal(err)
	}
	alreadyArchived, err := store.SaveMetadata(ctx, archive, Email{UID: 3, Subject: "Old", Snippet: "333", ToAddrs: "[]", CCAddrs: "[]", Date: 3})
	if err != nil {
		t.Fatal(err)
	}
	read := true
	updated, err := store.QueueFlagsBatch(ctx, []int64{e1.ID, e2.ID, e1.ID, 999999}, FlagChange{IsRead: &read})
	if err != nil || len(updated) != 2 {
		t.Fatalf("QueueFlagsBatch read failed: len=%d err=%v", len(updated), err)
	}
	// Archive skips alreadyArchived and coalesces onto e1 and e2.
	archived, err := store.QueueFlagsBatch(ctx, []int64{e1.ID, e2.ID, alreadyArchived.ID}, FlagChange{Archive: true})
	if err != nil || len(archived) != 2 {
		t.Fatalf("QueueFlagsBatch archive failed: len=%d err=%v", len(archived), err)
	}
	// Further changes on e1/e2 are blocked by ErrActionPending.
	if _, err := store.QueueFlagsBatch(ctx, []int64{e1.ID, e2.ID}, FlagChange{Delete: true}); !errors.Is(err, ErrActionPending) {
		t.Fatalf("expected ErrActionPending, got %v", err)
	}
	// If only alreadyArchived is targeted for archive, ErrInvalidQuery is returned.
	if _, err := store.QueueFlagsBatch(ctx, []int64{alreadyArchived.ID}, FlagChange{Archive: true}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("expected ErrInvalidQuery for already archived, got %v", err)
	}
	// Batch trash metadata removal removes multiple UIDs in one transaction.
	t1, err := store.SaveMetadata(ctx, trash, Email{UID: 10, Subject: "Trash 1", ToAddrs: "[]", CCAddrs: "[]", Date: 10})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := store.SaveMetadata(ctx, trash, Email{UID: 11, Subject: "Trash 2", ToAddrs: "[]", CCAddrs: "[]", Date: 11})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveTrashMetadataBatch(ctx, a.ID, "Trash", 3, []uint32{t1.UID, t2.UID}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListEmails(ctx, EmailQuery{Folder: "trash"})
	if err != nil || page.Total != 0 {
		t.Fatalf("expected empty trash after batch delete, got total=%d err=%v", page.Total, err)
	}
}

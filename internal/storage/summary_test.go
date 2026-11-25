package storage

import (
	"context"
	"testing"
)

func TestMailSummaryCountsAllLocalFolders(t *testing.T) {
	s, a, _, _ := cachedBodyFixture(t)
	ctx := context.Background()
	b, err := s.SaveAccount(ctx, Account{Email: "empty@example.com", Provider: "custom", AuthType: "password", IMAPHost: "fixture", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "fixture", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture"), ProxyMode: "direct", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.MailSummary(ctx)
	if err != nil || summary.Total != 1 || summary.Unread != 1 || len(summary.Accounts) != 2 || summary.Accounts[1].Total != 0 {
		t.Fatal("empty account or initial summary incorrect", err)
	}
	trash, err := s.PrepareFolder(ctx, b.ID, "Trash", "trash", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMetadata(ctx, trash, Email{UID: 1, IsRead: true, ToAddrs: "[]", CCAddrs: "[]", Date: 1}); err != nil {
		t.Fatal(err)
	}
	summary, err = s.MailSummary(ctx)
	if err != nil || summary.Total != 2 || summary.Unread != 1 || summary.Accounts[1].Total != 1 {
		t.Fatal("trash/local counts omitted", err)
	}
	if err := s.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	summary, err = s.MailSummary(ctx)
	if err != nil || summary.Total != 1 || summary.Unread != 0 {
		t.Fatal("deleted account count retained", err)
	}
	path, err := s.DatabasePath(ctx)
	if err != nil || path == "" {
		t.Fatal("database path unavailable", err)
	}
}

func TestCategoryCountsFollowPendingMoves(t *testing.T) {
	store, account, _, email := cachedBodyFixture(t)
	ctx := context.Background()
	check := func(folder string, unread bool, want int64) {
		t.Helper()
		summary, err := store.MailSummary(ctx)
		if err != nil {
			t.Fatal(err)
		}
		page, err := store.ListEmails(ctx, EmailQuery{Folder: folder, Unread: unread})
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		switch {
		case unread:
			count = summary.Categories.Unread
		case folder == "inbox":
			count = summary.Categories.Inbox
		case folder == "all":
			count = summary.Categories.All
		case folder == "archive":
			count = summary.Categories.Archive
		case folder == "trash":
			count = summary.Categories.Trash
		}
		if count != want || page.Total != want {
			t.Fatalf("category %s unread=%v: summary=%d list=%d want=%d", folder, unread, count, page.Total, want)
		}
		if len(summary.Accounts) != 1 || summary.Accounts[0].ID != account.ID || summary.Accounts[0].Categories != summary.Categories {
			t.Fatal("per-account count mismatch")
		}
	}
	check("inbox", false, 1)
	check("all", true, 1)
	if _, err := store.QueueFlags(ctx, email.ID, FlagChange{Delete: true}); err != nil {
		t.Fatal(err)
	}
	check("inbox", false, 0)
	check("all", true, 0)
	check("trash", false, 1)
	actions, err := store.FlagActions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trash, err := store.PrepareFolder(ctx, account.ID, "Trash", "trash", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishFlagAction(ctx, actions[0], &trash, 1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.QueueFlags(ctx, email.ID, FlagChange{Restore: true}); err != nil {
		t.Fatal(err)
	}
	check("inbox", false, 1)
	check("all", true, 1)
	check("trash", false, 0)
	actions, err = store.FlagActions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishFlagAction(ctx, actions[0], nil, 0, true); err != nil {
		t.Fatal(err)
	}
	check("trash", false, 1)
	check("inbox", false, 0)
}

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

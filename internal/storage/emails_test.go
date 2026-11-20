package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestUnifiedInboxSearchAndCursor(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	var accounts []Account
	for i := 0; i < 2; i++ {
		a, err := s.SaveAccount(ctx, Account{Email: fmt.Sprintf("account%d@example.com", i), Provider: "custom", AuthType: "password", IMAPHost: "imap.example.com", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "smtp.example.com", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("test ciphertext"), ProxyMode: "direct", Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, a)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		if _, err := tx.ExecContext(ctx, "INSERT INTO folders(account_id,name,role,uid_validity) VALUES(?,'INBOX','inbox',1)", a.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2000; i++ {
		a := accounts[i%2]
		if _, err := tx.ExecContext(ctx, `INSERT INTO emails(account_id,folder_id,uid,subject,from_name,from_addr,snippet,is_read,date,created_at) VALUES(?,(SELECT id FROM folders WHERE account_id=?),?,?,?,?,?,?,?,?)`, a.ID, a.ID, i+1, fmt.Sprintf("邮件收件通知 verification code %d", i), "Sender", "sender@example.com", "您的验证码为 123456，欢迎使用 MeldMail", i%2, 1000+i/2, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"收件通知", "fication", "123456", "中文不存在", "验证码", "12", "%", "\" OR 1=1 --"} {
		start := time.Now()
		page, err := s.ListEmails(ctx, EmailQuery{Q: query, Limit: 50})
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if !raceEnabled && elapsed >= 50*time.Millisecond {
			t.Fatalf("%q search took %s", query, elapsed)
		}
		want := 50
		if query == "中文不存在" || query == "%" || query == "\" OR 1=1 --" {
			want = 0
		}
		if len(page.Emails) != want {
			t.Fatalf("%q got %d", query, len(page.Emails))
		}
		t.Logf("%q: %s", query, elapsed)
	}
	first, err := s.ListEmails(ctx, EmailQuery{Limit: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Emails) != 7 || first.NextCursor == "" {
		t.Fatal("pagination missing")
	}
	second, err := s.ListEmails(ctx, EmailQuery{Limit: 7, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[int64]bool)
	for _, page := range []EmailPage{first, second} {
		for _, e := range page.Emails {
			if seen[e.ID] {
				t.Fatal("duplicate cursor item")
			}
			seen[e.ID] = true
		}
	}
	last := first.Emails[len(first.Emails)-1]
	next := second.Emails[0]
	if next.Date > last.Date || next.Date == last.Date && next.ID >= last.ID {
		t.Fatal("incorrect keyset order")
	}
	filtered, err := s.ListEmails(ctx, EmailQuery{AccountID: accounts[0].ID, Unread: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range filtered.Emails {
		if e.AccountID != accounts[0].ID || e.IsRead {
			t.Fatal("filter failed")
		}
	}
	readOnly, err := s.ListEmails(ctx, EmailQuery{AccountID: accounts[1].ID, Unread: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(readOnly.Emails) != 0 {
		t.Fatal("unread filter leaked read emails")
	}
	if _, err := s.ListEmails(ctx, EmailQuery{Cursor: "malformed"}); err == nil {
		t.Fatal("accepted bad cursor")
	}
}

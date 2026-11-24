package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func cachedBodyFixture(t *testing.T) (*Store, Account, Folder, Email) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "bodies.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	})
	a, err := store.SaveAccount(ctx, Account{Email: "cache@example.com", Provider: "custom", AuthType: "password", IMAPHost: "fixture", IMAPPort: 993, IMAPTLS: "implicit", SMTPHost: "fixture", SMTPPort: 465, SMTPTLS: "implicit", CredentialsEnc: []byte("fixture"), ProxyMode: "direct", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.PrepareFolder(ctx, a.ID, "INBOX", "inbox", 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := store.SaveMetadata(ctx, f, Email{UID: 1, ToAddrs: "[]", CCAddrs: "[]", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	return store, a, f, e
}
func TestBodyCacheInvalidationAndLimits(t *testing.T) {
	s, a, f, e := cachedBodyFixture(t)
	ctx := context.Background()
	body := CachedBody{HTML: "<p>cached</p>", AttachmentsJSON: "[]"}
	save := func(email Email) {
		t.Helper()
		if err := s.SaveBody(ctx, email, "v1", body); err != nil {
			t.Fatal(err)
		}
	}
	miss := func(id int64, key string) {
		t.Helper()
		_, hit, err := s.Body(ctx, id, key)
		if err != nil || hit {
			t.Fatalf("expected cache miss: hit=%v err=%v", hit, err)
		}
	}
	save(e)
	if got, hit, err := s.Body(ctx, e.ID, "v1"); err != nil || !hit || got != body {
		t.Fatal("cache round trip failed", err)
	}
	miss(e.ID, "changed-policy-or-source")
	if err := s.SaveBody(ctx, e, "v1", CachedBody{Text: strings.Repeat("x", MaxCachedBodyBytes+1), AttachmentsJSON: "[]"}); err == nil {
		t.Fatal("oversized body accepted")
	}
	if err := s.SaveBody(ctx, e, "v1", CachedBody{AttachmentsJSON: strings.Repeat("x", MaxCachedAttachmentMetadataBytes+1)}); err == nil {
		t.Fatal("oversized metadata accepted")
	}
	// UID moves invalidate the old part numbering and identity.
	if _, err := s.DB.ExecContext(ctx, "UPDATE emails SET uid=? WHERE id=?", 2, e.ID); err != nil {
		t.Fatal(err)
	}
	miss(e.ID, "v1")
	if err := s.SaveBody(ctx, e, "v1", body); err == nil {
		t.Fatal("in-flight stale source restored cached data")
	}
	e.UID = 2
	save(e)
	a.IMAPHost = "other-fixture"
	if _, err := s.SaveAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	miss(e.ID, "v1")
	save(e)
	if _, err := s.PrepareFolder(ctx, a.ID, "INBOX", "inbox", f.UIDValidity+1); err != nil {
		t.Fatal(err)
	}
	miss(e.ID, "v1")
	newer, err := s.SaveMetadata(ctx, Folder{ID: f.ID, AccountID: a.ID, Name: "INBOX", Role: "inbox", UIDValidity: 2}, Email{UID: 1, ToAddrs: "[]", CCAddrs: "[]", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	save(newer)
	if err := s.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	miss(newer.ID, "v1")
}
func TestBodyCacheByteBudget(t *testing.T) {
	s, _, f, e := cachedBodyFixture(t)
	ctx := context.Background()
	body := CachedBody{Text: strings.Repeat("x", MaxCachedBodyBytes), AttachmentsJSON: "[]"}
	for i := 1; i <= 22; i++ {
		if i > 1 {
			var err error
			e, err = s.SaveMetadata(ctx, f, Email{UID: uint32(i), ToAddrs: "[]", CCAddrs: "[]", Date: 1})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SaveBody(ctx, e, "v1", body); err != nil {
			t.Fatal(err)
		}
	}
	var size int64
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(size_bytes),0),COUNT(*) FROM email_bodies").Scan(&size, &count); err != nil {
		t.Fatal(err)
	}
	if size > BodyCacheBytes || size <= BodyCacheBytes-MaxCachedBodyBytes-MaxCachedAttachmentMetadataBytes || count >= 22 {
		t.Fatalf("invalid byte eviction: %d bytes, %d rows", size, count)
	}
	if _, hit, err := s.Body(ctx, e.ID, "v1"); err != nil || !hit {
		t.Fatal("latest body was evicted", err)
	}
	if _, hit, err := s.Body(ctx, 1, "v1"); err != nil || hit {
		t.Fatal("oldest body was retained", err)
	}
}
func TestBodyCacheEntryBudgetAndAccessOrder(t *testing.T) {
	s, a, f, e := cachedBodyFixture(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `WITH RECURSIVE n(x) AS (SELECT 2 UNION ALL SELECT x+1 FROM n WHERE x<?)
 INSERT INTO emails(account_id,folder_id,uid,date,created_at) SELECT ?,?,x,0,0 FROM n`, BodyCacheEntries+1, a.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO email_bodies(email_id,source_key,html,text,attachments_json,size_bytes,stored_at,last_access)
 SELECT id,'v1','','x','[]',3,0,id FROM emails`); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := s.Body(ctx, 2, "v1"); err != nil || !hit {
		t.Fatal("touch cache failed", err)
	}
	if err := s.SaveBody(ctx, e, "v1", CachedBody{Text: "x", AttachmentsJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM email_bodies").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != BodyCacheEntries {
		t.Fatalf("entry count %d", count)
	}
	if _, hit, err := s.Body(ctx, 2, "v1"); err != nil || !hit {
		t.Fatal("recently read entry was evicted", err)
	}
	if _, hit, err := s.Body(ctx, 3, "v1"); err != nil || hit {
		t.Fatal("oldest untouched entry not evicted", err)
	}
}

func TestBodyCacheMigrationPreservesExistingMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitPassword(ctx, "existing-password-hash"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"DROP TRIGGER emails_body_source_changed", "DROP TRIGGER accounts_body_source_changed", "DROP TABLE email_bodies"} {
		if _, err := store.DB.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	if got, err := store.Setting(ctx, "admin_password"); err != nil || got != "existing-password-hash" {
		t.Fatal("upgrade overwrote existing settings", err)
	}
	var rows int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM email_bodies").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("cache migration missing or nonempty", err)
	}
}

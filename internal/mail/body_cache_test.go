package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/emersion/go-imap/v2"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/nyakooo/meldmail/internal/testmail"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBodySavedAndReadLocallyAfterReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "cache@example.com", "fixture")
	raw := "From: sender@example.com\r\nSubject: cache fixture\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Local after first view</p><script>neverCacheThisScript()</script>"
	if _, err := fixture.User.Append("INBOX", strings.NewReader(raw), &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cache.db")
	store, err := storage.Open(ctx, path)
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
	a, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "cache@example.com", Provider: "custom", IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit", SMTPHost: "127.0.0.1", SMTPPort: 465, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "fixture"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Sync(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListEmails(ctx, storage.EmailQuery{})
	if err != nil || len(page.Emails) != 1 {
		t.Fatal("missing metadata", err)
	}
	id := page.Emails[0].ID
	var rows int
	if err := store.DB.QueryRowContext(ctx, "SELECT count(*) FROM email_bodies").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("background sync cached complete bodies", err)
	}
	first, err := service.Read(ctx, id)
	if err != nil || !first.Cached || first.Local || first.CacheWarning != "" {
		t.Fatalf("first read did not save: %v %s", err, first.CacheWarning)
	}
	var persisted string
	if err := store.DB.QueryRowContext(ctx, "SELECT html FROM email_bodies WHERE email_id=?", id).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(persisted, "Local after first view") || strings.Contains(persisted, "neverCacheThisScript") {
		t.Fatal("stored body was not sanitized")
	}
	if err := store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	service, err = New(store, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	var network atomic.Int64
	service.TLSConfig = fixture.ClientTLS.Clone()
	service.TLSConfig.VerifyConnection = func(tls.ConnectionState) error {
		network.Add(1)
		return errors.New("network unavailable for cache acceptance")
	}
	// Even occupied network slots cannot block a local cached read.
	for i := 0; i < 3; i++ {
		service.interactive <- struct{}{}
	}
	second, err := service.Read(ctx, id)
	for i := 0; i < 3; i++ {
		<-service.interactive
	}
	if err != nil || !second.Local || second.HTML != first.HTML || network.Load() != 0 {
		t.Fatalf("reopened service accessed network or lost body: %v", err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- service.ReadTo(ctx, id, func(EmailContent) error { close(started); <-release; return nil })
	}()
	<-started
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	_, blocked := service.Read(short, id)
	stop()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(blocked, context.DeadlineExceeded) {
		t.Fatal("body slot was released before response consumption")
	}
	if _, err := store.DB.ExecContext(ctx, `CREATE TRIGGER fail_touch BEFORE UPDATE OF last_access ON email_bodies BEGIN SELECT RAISE(FAIL,'fixture readonly cache'); END`); err != nil {
		t.Fatal(err)
	}
	maintenance, err := service.Read(ctx, id)
	if err != nil || !maintenance.Local || !maintenance.Cached || maintenance.CacheWarning == "" || network.Load() != 0 {
		t.Fatal("cache maintenance failure prevented local reading", err)
	}
	if _, err := store.DB.ExecContext(ctx, "DROP TRIGGER fail_touch"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, "DELETE FROM email_bodies WHERE email_id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx, `CREATE TRIGGER fail_cache BEFORE INSERT ON email_bodies BEGIN SELECT RAISE(FAIL,'fixture cache unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	service.TLSConfig = fixture.ClientTLS
	warning, err := service.Read(ctx, id)
	if err != nil || warning.Cached || warning.CacheWarning == "" || warning.HTML != first.HTML {
		t.Fatal("cache write failure prevented reading or was hidden", err)
	}
}

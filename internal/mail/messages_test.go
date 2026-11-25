package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/nyakooo/meldmail/internal/testmail"
)

func TestReadSanitizeAndDecodeAttachment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "read.db"))
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
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := cert.TLS.Clone()
	roots := x509.NewCertPool()
	roots.AddCert(cert.Certificate())
	cert.Close()
	service.TLSConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	backend := imapmemserver.New()
	user := imapmemserver.NewUser("reader@example.com", "secret")
	backend.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	attachment := []byte{0, 1, 2, 3, 255, 254, 13, 10}
	html := `<h1>Hello 中文</h1><script>alert(document.cookie)</script><img src="https://tracker.example/pixel"><a href="javascript:alert(1)">bad</a><p onclick="attack()">Safe content</p>`
	raw := "From: sender@example.com\r\nTo: reader@example.com\r\nSubject: HTML fixture\r\nMessage-ID: <original@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n--inner\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain fallback\r\n--inner\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + strings.ReplaceAll(html, "=", "=3D") + "\r\n--inner--\r\n--outer\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=sample.bin\r\nContent-Transfer-Encoding: base64\r\n\r\n" + base64.StdEncoding.EncodeToString(attachment) + "\r\n--outer--\r\n"
	for _, body := range []string{raw, "From: sender@example.com\r\nSubject: oversized\r\nContent-Type: text/plain\r\n\r\n" + strings.Repeat("x", int(imapclient.MaxBodyBytes)+1)} {
		if _, err := user.Append("INBOX", strings.NewReader(body), &imap.AppendOptions{Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	server := imapserver.New(&imapserver.Options{TLSConfig: cfg, Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}}, NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return backend.NewSession(), nil, nil
	}})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "reader@example.com", Provider: "custom", IMAPHost: host, IMAPPort: number, IMAPTLS: "implicit", SMTPHost: host, SMTPPort: 465, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "secret"}, nil)
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
	page, err := store.ListEmails(ctx, storage.EmailQuery{Q: "HTML fixture"})
	if err != nil || len(page.Emails) != 1 {
		t.Fatal("fixture missing", err)
	}
	e := page.Emails[0]
	content, err := service.Read(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.HTML, "Hello 中文") || !strings.Contains(content.HTML, "Safe content") {
		t.Fatal("HTML body not selected/decoded", content.HTML)
	}
	for _, unsafe := range []string{"script", "onclick", "javascript:", "tracker.example", "document.cookie"} {
		if strings.Contains(content.HTML, unsafe) {
			t.Fatalf("unsafe HTML retained: %s", unsafe)
		}
	}
	if len(content.Attachments) != 1 || content.Attachments[0].Filename != "sample.bin" {
		t.Fatalf("wrong attachments: %+v", content.Attachments)
	}
	var downloaded bytes.Buffer
	if err := service.Download(ctx, e.ID, content.Attachments[0].ID, func(part imapclient.Part, reader io.Reader) error { _, err := io.Copy(&downloaded, reader); return err }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded.Bytes(), attachment) {
		t.Fatal("attachment decoding corrupted bytes")
	}
	if err := service.Download(ctx, e.ID, "1.2", func(part imapclient.Part, reader io.Reader) error { return nil }); !errors.Is(err, imapclient.ErrMessageGone) {
		t.Fatal("body part allowed as attachment", err)
	}
	oversized, err := store.ListEmails(ctx, storage.EmailQuery{Q: "oversized"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(ctx, oversized.Emails[0].ID); !errors.Is(err, imapclient.ErrBodyTooLarge) {
		t.Fatal("oversized body accepted", err)
	}

	smtpPort, deliveries := testmail.SMTP(t, cfg)
	a.SMTPPort = smtpPort
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"send", "reply", "forward"} {
		input := SendInput{AccountID: a.ID, Action: action, EmailID: e.ID, Text: "A new message"}
		if action != "reply" {
			input.To = []string{"recipient@example.com"}
		}
		if err := service.Send(ctx, input); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		delivered := <-deliveries
		parsed, err := netmail.ReadMessage(bytes.NewReader(delivered))
		if err != nil {
			t.Fatal(err)
		}
		if action == "reply" && parsed.Header.Get("In-Reply-To") != "<original@example.com>" {
			t.Fatal("reply threading header missing")
		}
		if action == "forward" {
			media, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
			if err != nil || !strings.HasPrefix(media, "multipart/") {
				t.Fatal("forward lost attachment", err)
			}
			parts := multipart.NewReader(parsed.Body, params["boundary"])
			found := false
			for {
				part, err := parts.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if part.FileName() == "sample.bin" {
					var b bytes.Buffer
					if _, err := io.Copy(&b, base64.NewDecoder(base64.StdEncoding, part)); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(b.Bytes(), attachment) {
						t.Fatal("forwarded attachment corrupted")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("forwarded attachment missing")
			}
		}
	}
	read, star := true, true
	if _, err := service.ChangeFlags(ctx, e.ID, storage.FlagChange{IsRead: &read, IsStarred: &star}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ChangeFlags(ctx, e.ID, storage.FlagChange{Archive: true}); !errors.Is(err, storage.ErrActionPending) {
		t.Fatal("pending action overwritten", err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Email(ctx, e.ID)
	if err != nil || !saved.IsRead || !saved.IsStarred {
		t.Fatal("optimistic flags not retained", err)
	}
	if _, err := service.ChangeFlags(ctx, e.ID, storage.FlagChange{Archive: true}); err != nil {
		t.Fatal(err)
	}
	inbox, err := store.ListEmails(ctx, storage.EmailQuery{Q: "HTML fixture"})
	if err != nil || len(inbox.Emails) != 0 {
		t.Fatal("archive not optimistic", err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	moved, err := store.Email(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := store.Folder(ctx, moved.FolderID)
	if err != nil || folder.Role != "archive" {
		t.Fatal("archive destination not stored", err)
	}
	if _, err := service.Read(ctx, e.ID); err != nil {
		t.Fatal("archived message unreadable", err)
	}
	if _, err := service.ChangeFlags(ctx, e.ID, storage.FlagChange{Delete: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	moved, err = store.Email(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	folder, err = store.Folder(ctx, moved.FolderID)
	if err != nil || folder.Role != "trash" {
		t.Fatal("trash destination not stored", err)
	}

	if _, err := service.ChangeFlags(ctx, e.ID, storage.FlagChange{Restore: true}); err != nil {
		t.Fatal(err)
	}
	trash, err := store.ListEmails(ctx, storage.EmailQuery{Folder: "trash"})
	if err != nil || trash.Total != 0 {
		t.Fatal("pending restore still visible in trash", err)
	}
	restoredInbox, err := store.ListEmails(ctx, storage.EmailQuery{Folder: "inbox"})
	foundRestore := false
	for _, item := range restoredInbox.Emails {
		if item.ID == e.ID && item.FolderRole == "inbox" && item.PendingAction == "restore" {
			foundRestore = true
		}
	}
	if err != nil || !foundRestore {
		t.Fatal("pending restore missing from inbox", err)
	}
	if err := service.ProcessFlagActions(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Email(ctx, e.ID)
	if err != nil || restored.FolderRole != "inbox" || restored.UID == 0 {
		t.Fatal("restore destination not stored", err)
	}
	actions, err := store.FlagActions(ctx)
	if err != nil || len(actions) != 0 {
		t.Fatal("restore not confirmed", err)
	}
	if _, err := service.Read(ctx, e.ID); err != nil {
		t.Fatal("restored email unreadable", err)
	}
	if _, err := service.ChangeFlags(ctx, e.ID, storage.FlagChange{Restore: true}); !errors.Is(err, storage.ErrInvalidQuery) {
		t.Fatal("restore outside trash/archive accepted", err)
	}
	// The on-demand body never becomes searchable or persisted.
	persisted, err := store.Email(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted.Snippet, "<script>") {
		t.Fatal("HTML persisted in metadata")
	}
}

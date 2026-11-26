package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestSyncAndDiagnostics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "sync.db"))
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
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	serverTLS := certServer.TLS.Clone()
	pool := x509.NewCertPool()
	pool.AddCert(certServer.Certificate())
	certServer.Close()
	service.TLSConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	backend := imapmemserver.New()
	user := imapmemserver.NewUser("test@example.com", "test-secret")
	backend.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	appendMail := func(subject string) {
		t.Helper()
		body := "From: Sender <sender@example.com>\r\nTo: test@example.com\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.Repeat("好", 600)
		if _, err := user.Append("INBOX", strings.NewReader(body), &imap.AppendOptions{Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 105; i++ {
		appendMail(fmt.Sprintf("message %03d", i))
	}
	server := imapserver.New(&imapserver.Options{NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return backend.NewSession(), nil, nil
	}, TLSConfig: serverTLS})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
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
	smtpListener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	smtpDone := make(chan error, 1)
	go func() {
		c, err := smtpListener.Accept()
		if err != nil {
			smtpDone <- err
			return
		}
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := fmt.Fprint(c, "220 test SMTP\r\n"); err != nil {
			smtpDone <- err
			return
		}
		reader := bufio.NewReader(c)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				smtpDone <- nil
				return
			}
			response := "250 OK\r\n"
			if strings.HasPrefix(line, "EHLO") {
				response = "250-test\r\n250 AUTH PLAIN LOGIN\r\n"
			} else if strings.HasPrefix(line, "AUTH PLAIN") {
				response = "235 authenticated\r\n"
			} else {
				smtpDone <- fmt.Errorf("unexpected SMTP command (must not send)")
				return
			}
			if _, err := fmt.Fprint(c, response); err != nil {
				smtpDone <- err
				return
			}
		}
	}()
	defer func() {
		if err := smtpListener.Close(); err != nil {
			t.Error(err)
		}
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	imapPort, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	shost, sport, err := net.SplitHostPort(smtpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	smtpPort, err := strconv.Atoi(sport)
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "test@example.com", Provider: "custom", IMAPHost: host, IMAPPort: imapPort, IMAPTLS: "implicit", SMTPHost: shost, SMTPPort: smtpPort, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "test-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(a.CredentialsEnc), "test-secret") {
		t.Fatal("plaintext credentials")
	}
	for _, diagnostic := range service.Test(ctx, a) {
		if !diagnostic.OK {
			t.Fatalf("diagnostic %+v", diagnostic)
		}
	}
	if err := <-smtpDone; err != nil {
		t.Fatal(err)
	}
	// A separate plaintext listener must upgrade before the same credentials work.
	plainListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	plainDone := make(chan error, 1)
	go func() { plainDone <- server.Serve(plainListener) }()
	defer func() {
		if err := plainListener.Close(); err != nil {
			t.Error(err)
		}
		if err := <-plainDone; err != nil {
			t.Error(err)
		}
	}()
	startTLSAccount := a
	phost, pport, err := net.SplitHostPort(plainListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	pnumber, err := strconv.Atoi(pport)
	if err != nil {
		t.Fatal(err)
	}
	startTLSAccount.IMAPHost = phost
	startTLSAccount.IMAPPort = pnumber
	startTLSAccount.IMAPTLS = "starttls"
	credentials, err := service.Credentials(startTLSAccount)
	if err != nil {
		t.Fatal(err)
	}
	d, err := service.Dialer(ctx, startTLSAccount)
	if err != nil {
		t.Fatal(err)
	}
	startTLSClient, err := imapclient.Open(ctx, startTLSAccount, credentials, d, service.TLSConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := startTLSClient.Close(); err != nil {
		t.Fatal(err)
	}
	count, err := service.Sync(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("initial sync count %d", count)
	}
	var total, maxSnippet, minUID int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*),MAX(length(snippet)),MIN(uid) FROM emails").Scan(&total, &maxSnippet, &minUID); err != nil {
		t.Fatal(err)
	}
	if total != 100 || maxSnippet != 500 || minUID != 6 {
		t.Fatalf("metadata count=%d snippet=%d min UID=%d", total, maxSnippet, minUID)
	}
	count, err = service.Sync(ctx, a.ID)
	if err != nil || count != 0 {
		t.Fatalf("empty incremental %d %v", count, err)
	}
	appendMail("new incremental")
	count, err = service.Sync(ctx, a.ID)
	if err != nil || count != 1 {
		t.Fatalf("incremental %d %v", count, err)
	}
	if err := user.Delete("INBOX"); err != nil {
		t.Fatal(err)
	}
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	appendMail("after reset")
	count, err = service.Sync(ctx, a.ID)
	if err != nil || count != 1 {
		t.Fatalf("UIDVALIDITY reset %d %v", count, err)
	}
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM emails").Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("stale metadata remained: %d", total)
	}
	if err := service.SetGlobalProxy(ctx, "http://proxy-user:proxy-secret@127.0.0.1:8888"); err != nil {
		t.Fatal(err)
	}
	encrypted, err := store.Setting(ctx, "global_proxy_enc")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "proxy-secret") {
		t.Fatal("plaintext proxy")
	}
	endpoint, err := service.GlobalProxy(ctx)
	if err != nil || endpoint != "http://proxy-user:proxy-secret@127.0.0.1:8888" {
		t.Fatal("proxy roundtrip", err)
	}
}

func TestConnectionDiagnosticsCheckInbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fixture := testmail.IMAP(t, "test@example.com", "fixture")
	service, err := New(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service.TLSConfig = fixture.ClientTLS
	account, err := service.Prepare(ctx, AccountInput{
		Account: storage.Account{
			Email: "test@example.com", Provider: "custom", ProxyMode: "direct",
			IMAPHost: "127.0.0.1", IMAPPort: fixture.Port, IMAPTLS: "implicit",
			SMTPHost: "127.0.0.1", SMTPPort: fixture.Port, SMTPTLS: "implicit",
		},
		Password: "fixture",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.FailNextSelect("INBOX")
	checks := service.Test(ctx, account)
	if len(checks) < 2 || checks[1].Stage != "imap" || checks[1].OK || checks[1].Message != "IMAP 已认证，但无法读取收件箱" {
		t.Fatalf("expected inbox access failure, got %+v", checks)
	}
}

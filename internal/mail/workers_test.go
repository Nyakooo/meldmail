package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
)

type observedSession struct {
	imapserver.Session
	active *atomic.Int64
	idles  *atomic.Int64
}

func (s *observedSession) Close() error { defer s.active.Add(-1); return s.Session.Close() }
func (s *observedSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	s.idles.Add(1)
	return s.Session.Idle(w, stop)
}
func TestBackgroundIDLERecoveryAndReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "worker.db"))
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
	serverTLS := cert.TLS.Clone()
	pool := x509.NewCertPool()
	pool.AddCert(cert.Certificate())
	cert.Close()
	service.TLSConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	backend := imapmemserver.New()
	user := imapmemserver.NewUser("background@example.com", "secret")
	backend.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	appendMail := func(subject string) {
		t.Helper()
		raw := "From: sender@example.com\r\nTo: background@example.com\r\nSubject: " + subject + "\r\nContent-Type: text/plain\r\n\r\nbounded snippet"
		if _, err := user.Append("INBOX", strings.NewReader(raw), &imap.AppendOptions{Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	var active, idles atomic.Int64
	var mu sync.Mutex
	var conns []net.Conn
	server := imapserver.New(&imapserver.Options{TLSConfig: serverTLS, NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		mu.Lock()
		conns = append(conns, c.NetConn())
		mu.Unlock()
		active.Add(1)
		return &observedSession{Session: backend.NewSession(), active: &active, idles: &idles}, nil, nil
	}})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-serverDone; err != nil {
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
	a, err := service.Prepare(ctx, AccountInput{Account: storage.Account{Email: "background@example.com", Provider: "custom", IMAPHost: host, IMAPPort: number, IMAPTLS: "implicit", SMTPHost: host, SMTPPort: 465, SMTPTLS: "implicit", ProxyMode: "direct"}, Password: "secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err = store.SaveAccount(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	appendMail("browser closed")
	options := syncer.DefaultOptions()
	options.Backoff = []time.Duration{5 * time.Millisecond, 15 * time.Millisecond, 30 * time.Millisecond, 60 * time.Millisecond}
	options.ReconcileInterval = 100 * time.Millisecond
	manager := service.NewWorkers(options)
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- manager.Run(workerCtx) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait := func(message string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal(message)
	}
	count := func() int {
		var n int
		if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM emails").Scan(&n); err != nil {
			t.Error(err)
		}
		return n
	}
	wait("background sync needs no browser", func() bool { return count() == 1 && idles.Load() >= 1 })
	events, unsubscribe, err := service.Events.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	appendMail("IDLE new email")
	wait("IDLE notification did not persist new email", func() bool { return count() == 2 })
	found := false
drain:
	for {
		select {
		case event := <-events:
			if event.Type == "new_email" && strings.Contains(string(event.Data), "IDLE new email") {
				found = true
			}
		default:
			break drain
		}
	}
	if !found {
		t.Fatal("new mail SSE not published")
	}
	// Drop the live IMAP sockets; the listener stays available for automatic recovery.
	before := idles.Load()
	mu.Lock()
	dropped := append([]net.Conn(nil), conns...)
	mu.Unlock()
	for _, c := range dropped {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	}
	appendMail("after outage")
	wait("worker did not reconnect after network failure", func() bool { return idles.Load() > before && count() == 3 })
	credentials, err := service.Credentials(a)
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := service.Dialer(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	client, err := imapclient.Open(ctx, a, credentials, dialer, service.TLSConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.IMAP.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := client.IMAP.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	wait("remote read state not reconciled", func() bool {
		var read bool
		if err := store.DB.QueryRowContext(ctx, "SELECT is_read FROM emails WHERE uid=1").Scan(&read); err != nil {
			return false
		}
		return read
	})
	if err := client.IMAP.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.IMAP.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait("remote deletion not reconciled", func() bool { return count() == 2 })
	stop()
	wait("IMAP sessions leaked on shutdown", func() bool { return active.Load() == 0 })
}

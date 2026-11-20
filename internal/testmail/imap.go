package testmail

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type Fixture struct {
	failSelect           atomic.Pointer[string]
	User                 *imapmemserver.User
	Port                 int
	ServerTLS, ClientTLS *tls.Config
}

func IMAP(t *testing.T, email, password string) *Fixture {
	t.Helper()
	certificate := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := certificate.TLS.Clone()
	roots := x509.NewCertPool()
	roots.AddCert(certificate.Certificate())
	certificate.Close()
	user := imapmemserver.NewUser(email, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	fixture := &Fixture{User: user, ServerTLS: cfg, ClientTLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	server := imapserver.New(&imapserver.Options{TLSConfig: cfg, Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}}, NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return &faultSession{UserSession: imapmemserver.NewUserSession(user), fixture: fixture}, nil, nil
	}})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	fixture.Port = listener.Addr().(*net.TCPAddr).Port
	return fixture
}

func (f *Fixture) FailNextSelect(name string) { f.failSelect.Store(&name) }

type faultSession struct {
	*imapmemserver.UserSession
	fixture *Fixture
}

func (s *faultSession) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if fault := s.fixture.failSelect.Load(); fault != nil && *fault == name && s.fixture.failSelect.CompareAndSwap(fault, nil) {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "injected destination selection failure"}
	}
	return s.UserSession.Select(name, options)
}

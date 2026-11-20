package testmail

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type Fixture struct {
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
	backend := imapmemserver.New()
	user := imapmemserver.NewUser(email, password)
	backend.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return &Fixture{User: user, Port: listener.Addr().(*net.TCPAddr).Port, ServerTLS: cfg, ClientTLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
}

package smtpclient

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/proxy"
	"github.com/nyakooo/meldmail/internal/storage"
)

func TestMandatorySTARTTLS(t *testing.T) {
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := cert.TLS.Clone()
	roots := x509.NewCertPool()
	roots.AddCert(cert.Certificate())
	cert.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := fmt.Fprint(c, "220 ready\r\n"); err != nil {
			done <- err
			return
		}
		reader := bufio.NewReader(c)
		encrypted := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- nil
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				if _, err := fmt.Fprint(c, "250-test\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n"); err != nil {
					done <- err
					return
				}
			case strings.HasPrefix(line, "STARTTLS"):
				if _, err := fmt.Fprint(c, "220 TLS ready\r\n"); err != nil {
					done <- err
					return
				}
				secure := tls.Server(c, cfg)
				if err := secure.Handshake(); err != nil {
					done <- err
					return
				}
				c = secure
				reader = bufio.NewReader(secure)
				encrypted = true
			case strings.HasPrefix(line, "AUTH PLAIN"):
				if !encrypted {
					done <- fmt.Errorf("credentials sent before TLS")
					return
				}
				if _, err := fmt.Fprint(c, "235 authenticated\r\n"); err != nil {
					done <- err
					return
				}
			default:
				done <- fmt.Errorf("unexpected command")
				return
			}
		}
	}()
	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	d, err := proxy.New("direct", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := Open(ctx, storage.Account{Email: "test@example.com", SMTPHost: host, SMTPPort: number, SMTPTLS: "starttls"}, auth.Credentials{Password: "secret"}, d, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

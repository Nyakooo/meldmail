package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/proxy"
	"github.com/nyakooo/meldmail/internal/storage"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGmailArchiveRemovesInboxLabel(t *testing.T) {
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := cert.TLS.Clone()
	pool := x509.NewCertPool()
	pool.AddCert(cert.Certificate())
	cert.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan error, 1)
	removed := make(chan bool, 1)
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
		if _, err := fmt.Fprint(c, "* OK Gmail fixture\r\n"); err != nil {
			done <- err
			return
		}
		reader := bufio.NewReader(c)
		didRemove := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				removed <- didRemove
				if err == io.EOF {
					err = nil
				}
				done <- err
				return
			}
			fields := strings.Fields(line)
			response := fields[0] + " OK done\r\n"
			switch {
			case strings.Contains(line, " LOGIN "):
			case strings.Contains(line, ` SELECT "INBOX"`):
				response = "* OK [UIDVALIDITY 101] valid\r\n" + response
			case strings.Contains(line, ` SELECT "[Gmail]/All Mail"`):
				response = "* OK [UIDVALIDITY 202] valid\r\n" + response
			case strings.Contains(line, " UID FETCH 7 (X-GM-MSGID)"):
				response = "* 1 FETCH (UID 7 X-GM-MSGID 1234567890123456)\r\n" + response
			case strings.Contains(line, " UID SEARCH X-GM-MSGID 1234567890123456"):
				response = "* SEARCH 9\r\n" + response
			case strings.Contains(line, ` UID STORE 7 -X-GM-LABELS.SILENT (\Inbox)`):
				didRemove = true
			default:
				done <- fmt.Errorf("unexpected Gmail archive command")
				return
			}
			if _, err := fmt.Fprint(c, response); err != nil {
				done <- err
				return
			}
		}
	}()
	d, err := proxy.New("direct", "", "")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().(*net.TCPAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	uid, validity, err := GmailArchive(ctx, storage.Account{Email: "test@gmail.com", Provider: "gmail", IMAPHost: "127.0.0.1", IMAPPort: address.Port, IMAPTLS: "implicit"}, auth.Credentials{Password: "fixture"}, d, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, storage.Folder{Name: "INBOX", UIDValidity: 101}, 7, "[Gmail]/All Mail", "original@example.com", func(uint32, uint32, bool) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if uid != 9 || validity != 202 {
		t.Fatalf("destination %d/%d", validity, uid)
	}
	if !<-removed {
		t.Fatal("Inbox label was not removed")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

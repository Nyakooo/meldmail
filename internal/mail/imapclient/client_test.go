package imapclient

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

func TestProviderIDAndCancellation(t *testing.T) {
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := certServer.TLS.Clone()
	pool := x509.NewCertPool()
	pool.AddCert(certServer.Certificate())
	certServer.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	done := make(chan error, 1)
	observed := make(chan []string, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := fmt.Fprint(c, "* OK [CAPABILITY IMAP4rev1 ID] ready\r\n"); err != nil {
			done <- err
			return
		}
		reader := bufio.NewReader(c)
		var commands []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				observed <- commands
				done <- nil
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				done <- fmt.Errorf("malformed command")
				return
			}
			commands = append(commands, strings.TrimSpace(line))
			tag := fields[0]
			response := tag + " OK done\r\n"
			switch fields[1] {
			case "CAPABILITY":
				response = "* CAPABILITY IMAP4rev1 ID\r\n" + response
			case "LOGIN":
				response = tag + " OK [CAPABILITY IMAP4rev1 ID] logged in\r\n"
			case "ID":
				response = "* ID NIL\r\n" + response
			default:
				done <- fmt.Errorf("unexpected command %s", fields[1])
				return
			}
			if _, err := fmt.Fprint(c, response); err != nil {
				done <- err
				return
			}
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
	d, err := proxy.New("direct", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := Open(ctx, storage.Account{Email: "test@163.com", Provider: "163", IMAPHost: host, IMAPPort: number, IMAPTLS: "implicit"}, auth.Credentials{Password: "secret"}, d, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	commands := <-observed
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	login := -1
	id := -1
	for i, line := range commands {
		if strings.Contains(line, " LOGIN ") {
			login = i
		}
		if strings.Contains(line, " ID ") {
			id = i
			if !strings.Contains(line, `"name" "MeldMail" "version" "1.0" "vendor" "MeldMail"`) {
				t.Fatal("incorrect ID payload")
			}
		}
	}
	if login < 0 || id != login+1 {
		t.Fatalf("ID not immediately after LOGIN: %v", commands)
	}
}
func TestCancelWhileGreetingStalls(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()
	host, port, err := net.SplitHostPort(listener.Addr().String())
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
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	client, err := Open(ctx, storage.Account{IMAPHost: host, IMAPPort: number, IMAPTLS: "starttls"}, auth.Credentials{}, d, nil)
	if err == nil {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("accepted stalled greeting")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation too slow")
	}
	c := <-accepted
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}

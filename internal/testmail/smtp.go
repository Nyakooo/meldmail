// Package testmail supplies in-memory, loopback-only protocol fixtures.
package testmail

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

func SMTP(t *testing.T, cfg *tls.Config) (int, <-chan []byte) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := make(chan []byte, 10)
	var wg sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			c, err := ln.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Error(err)
				}
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
						t.Error(err)
					}
				}()
				if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
					t.Error(err)
					return
				}
				if _, err := fmt.Fprint(c, "220 fixture SMTP\r\n"); err != nil {
					t.Error(err)
					return
				}
				reader := bufio.NewReader(c)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
							t.Error(err)
						}
						return
					}
					response := "250 OK\r\n"
					switch {
					case strings.HasPrefix(line, "EHLO"):
						response = "250-fixture\r\n250 AUTH PLAIN\r\n"
					case strings.HasPrefix(line, "AUTH PLAIN"):
						response = "235 authenticated\r\n"
					case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
					case strings.TrimSpace(line) == "DATA":
						if _, err := fmt.Fprint(c, "354 send message\r\n"); err != nil {
							t.Error(err)
							return
						}
						var body bytes.Buffer
						if _, err := io.Copy(&body, textproto.NewReader(reader).DotReader()); err != nil {
							t.Error(err)
							return
						}
						deliveries <- append([]byte(nil), body.Bytes()...)
					default:
						t.Errorf("unexpected SMTP command")
						return
					}
					if _, err := fmt.Fprint(c, response); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Error(err)
		}
		<-stopped
		wg.Wait()
	})
	return ln.Addr().(*net.TCPAddr).Port, deliveries
}

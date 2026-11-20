package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func closeConn(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Error(err)
	}
}
func TestDirectAndCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(t, ln)
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer closeConn(t, c)
		_, err = c.Write([]byte("direct"))
		done <- err
	}()
	d, err := New("global", "", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if _, err := io.Copy(&body, c); err != nil {
		t.Fatal(err)
	}
	closeConn(t, c)
	if body.String() != "direct" {
		t.Fatal(body.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, err := d.DialContext(ctx, "tcp", ln.Addr().String()); err == nil {
		closeConn(t, c)
		t.Fatal("ignored cancellation")
	}
}
func TestCONNECT(t *testing.T) {
	payload := bytes.Repeat([]byte("tunnel"), 12000)
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint(secure), func(t *testing.T) {
			authSeen := make(chan bool, 1)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authSeen <- r.Method == "CONNECT" && r.Host == "mail.example:993" && r.Header.Get("Proxy-Authorization") == "Basic "+base64.StdEncoding.EncodeToString([]byte("user:secret"))
				conn, rw, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer closeConn(t, conn)
				if _, err := rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
					t.Error(err)
					return
				}
				if err := rw.Flush(); err != nil {
					t.Error(err)
					return
				}
				if _, err := conn.Write(payload); err != nil {
					t.Error(err)
				}
			})
			var server *httptest.Server
			if secure {
				server = httptest.NewTLSServer(handler)
			} else {
				server = httptest.NewServer(handler)
			}
			defer server.Close()
			scheme := "http"
			if secure {
				scheme = "https"
			}
			endpoint := scheme + "://user:secret@" + server.Listener.Addr().String()
			d, err := New("global", "", endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if secure {
				pool := x509.NewCertPool()
				pool.AddCert(server.Certificate())
				d.tlsConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
			}
			c, err := d.DialContext(context.Background(), "tcp", "mail.example:993")
			if err != nil {
				t.Fatal(err)
			}
			defer closeConn(t, c)
			var b bytes.Buffer
			if _, err := io.Copy(&b, c); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b.Bytes(), payload) {
				t.Fatal("tunnel stream truncated")
			}
			if !<-authSeen {
				t.Fatal("invalid CONNECT/auth")
			}
		})
	}
}
func TestSOCKS5Authentication(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(t, ln)
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer closeConn(t, conn)
		read := func(n int) ([]byte, error) { b := make([]byte, n); _, err := io.ReadFull(conn, b); return b, err }
		greeting, err := read(2)
		if err != nil {
			done <- err
			return
		}
		if _, err = read(int(greeting[1])); err != nil {
			done <- err
			return
		}
		if _, err = conn.Write([]byte{5, 2}); err != nil {
			done <- err
			return
		}
		auth, err := read(2)
		if err != nil {
			done <- err
			return
		}
		user, err := read(int(auth[1]))
		if err != nil {
			done <- err
			return
		}
		size, err := read(1)
		if err != nil {
			done <- err
			return
		}
		password, err := read(int(size[0]))
		if err != nil {
			done <- err
			return
		}
		if string(user) != "user" || string(password) != "secret" {
			done <- fmt.Errorf("wrong SOCKS5 credentials")
			return
		}
		if _, err = conn.Write([]byte{1, 0}); err != nil {
			done <- err
			return
		}
		command, err := read(5)
		if err != nil {
			done <- err
			return
		}
		if command[0] != 5 || command[1] != 1 || command[3] != 3 {
			done <- fmt.Errorf("wrong SOCKS request")
			return
		}
		target, err := read(int(command[4]) + 2)
		if err != nil {
			done <- err
			return
		}
		if string(target[:len(target)-2]) != "mail.example" {
			done <- fmt.Errorf("wrong SOCKS target")
			return
		}
		_, err = conn.Write(append([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}, []byte("socks")...))
		done <- err
	}()
	d, err := New("socks5", "socks5://user:secret@"+ln.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, err := d.DialContext(ctx, "tcp", "mail.example:993")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if _, err := io.Copy(&b, c); err != nil {
		t.Fatal(err)
	}
	closeConn(t, c)
	if b.String() != "socks" {
		t.Fatal(b.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestCONNECTFailures(t *testing.T) {
	for _, status := range []int{407, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer srv.Close()
			d, err := New("http", srv.URL, "")
			if err != nil {
				t.Fatal(err)
			}
			if c, err := d.DialContext(context.Background(), "tcp", "mail.example:993"); err == nil {
				closeConn(t, c)
				t.Fatal("accepted rejection")
			}
		})
	}
	for _, endpoint := range []string{"ftp://host", "http://", "http://host/path", "http://host:99999"} {
		if _, err := New("global", "", endpoint); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

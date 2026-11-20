// Package proxy provides the sole network dialer used by mail protocols.
package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type Dialer struct {
	mode      string
	endpoint  *url.URL
	tlsConfig *tls.Config
}

func New(mode, endpoint, global string) (*Dialer, error) {
	if mode == "" || mode == "global" {
		endpoint = global
		mode = "direct"
		if endpoint != "" {
			u, err := url.Parse(endpoint)
			if err != nil {
				return nil, fmt.Errorf("proxy: invalid global URL")
			}
			mode = u.Scheme
			if mode != "http" && mode != "https" && mode != "socks5" {
				return nil, fmt.Errorf("proxy: invalid global URL scheme")
			}
		}
	}
	if mode == "direct" {
		return &Dialer{mode: mode}, nil
	}
	if mode != "http" && mode != "https" && mode != "socks5" {
		return nil, fmt.Errorf("proxy: unsupported mode")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != mode || u.Hostname() == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("proxy: invalid URL")
	}
	if u.Port() == "" {
		port := "1080"
		if mode == "http" {
			port = "80"
		}
		if mode == "https" {
			port = "443"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	if _, err := net.LookupPort("tcp", u.Port()); err != nil {
		return nil, fmt.Errorf("proxy: invalid port")
	}
	return &Dialer{mode: mode, endpoint: u}, nil
}
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	base := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	if d.mode == "direct" {
		return base.DialContext(ctx, network, addr)
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("proxy: TCP required")
	}
	if d.mode == "socks5" {
		var auth *xproxy.Auth
		if d.endpoint.User != nil {
			password, ok := d.endpoint.User.Password()
			if !ok {
				password = ""
			}
			auth = &xproxy.Auth{User: d.endpoint.User.Username(), Password: password}
		}
		dialer, err := xproxy.SOCKS5("tcp", d.endpoint.Host, auth, base)
		if err != nil {
			return nil, fmt.Errorf("proxy SOCKS5 initialize: %w", err)
		}
		contextual, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("proxy: SOCKS5 context unavailable")
		}
		return contextual.DialContext(ctx, network, addr)
	}
	conn, err := base.DialContext(ctx, network, d.endpoint.Host)
	if err != nil {
		return nil, fmt.Errorf("proxy connect: %w", err)
	}
	fail := func(cause error) (net.Conn, error) {
		if err := conn.Close(); err != nil {
			return nil, fmt.Errorf("proxy close after %v: %w", cause, err)
		}
		return nil, cause
	}
	raw := conn
	stop := context.AfterFunc(ctx, func() {
		if err := raw.SetDeadline(time.Now()); err != nil {
			return
		}
	})
	defer stop()
	deadline, ok := ctx.Deadline()
	if ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fail(fmt.Errorf("proxy deadline: %w", err))
		}
	}
	if d.mode == "https" {
		cfg := &tls.Config{ServerName: d.endpoint.Hostname(), MinVersion: tls.VersionTLS12}
		if d.tlsConfig != nil {
			cfg = d.tlsConfig.Clone()
			cfg.ServerName = d.endpoint.Hostname()
		}
		secure := tls.Client(conn, cfg)
		if err := secure.HandshakeContext(ctx); err != nil {
			return fail(fmt.Errorf("proxy TLS: %w", err))
		}
		conn = secure
	}
	if strings.ContainsAny(addr, "\r\n") {
		return fail(fmt.Errorf("proxy: invalid target"))
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: make(http.Header)}
	if d.endpoint.User != nil {
		password, ok := d.endpoint.User.Password()
		if !ok {
			password = ""
		}
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(d.endpoint.User.Username()+":"+password)))
	}
	if err := req.Write(conn); err != nil {
		return fail(fmt.Errorf("proxy CONNECT write: %w", err))
	}
	budget := &handshakeReader{reader: conn, remaining: 32 * 1024, limited: true}
	reader := bufio.NewReaderSize(budget, 4096)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return fail(fmt.Errorf("proxy CONNECT response: %w", err))
	}
	if response.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("proxy CONNECT rejected: HTTP %d", response.StatusCode))
	}
	budget.limited = false
	if !stop() && ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fail(fmt.Errorf("proxy reset deadline: %w", err))
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

type handshakeReader struct {
	reader    io.Reader
	remaining int
	limited   bool
}

func (r *handshakeReader) Read(b []byte) (int, error) {
	if r.limited {
		if r.remaining <= 0 {
			return 0, fmt.Errorf("proxy: CONNECT headers exceed 32KiB")
		}
		if len(b) > r.remaining {
			b = b[:r.remaining]
		}
	}
	n, err := r.reader.Read(b)
	if r.limited {
		r.remaining -= n
	}
	return n, err
}

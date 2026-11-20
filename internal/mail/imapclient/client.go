package imapclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/emersion/go-imap/v2"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/adapter"
	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/storage"
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}
type Client struct {
	IMAP *upstream.Client
	conn net.Conn
	stop func() bool
}

func Open(ctx context.Context, a storage.Account, credentials auth.Credentials, d Dialer, tlsConfig *tls.Config) (result *Client, err error) {
	return open(ctx, a, credentials, d, tlsConfig, nil)
}
func open(ctx context.Context, a storage.Account, credentials auth.Credentials, d Dialer, tlsConfig *tls.Config, handler *upstream.UnilateralDataHandler) (result *Client, err error) {
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(a.IMAPHost, strconv.Itoa(a.IMAPPort)))
	if err != nil {
		return nil, fmt.Errorf("imap dial: %w", err)
	}
	raw := conn
	stop := context.AfterFunc(ctx, func() {
		if err := raw.Close(); err != nil {
			return
		}
	})
	defer func() {
		if err != nil {
			stop()
			if e := raw.Close(); e != nil && !errors.Is(e, net.ErrClosed) {
				err = errors.Join(err, fmt.Errorf("imap cleanup: %w", e))
			}
		}
	}()
	cfg := &tls.Config{ServerName: a.IMAPHost, MinVersion: tls.VersionTLS12}
	if tlsConfig != nil {
		cfg = tlsConfig.Clone()
		cfg.ServerName = a.IMAPHost
	}
	options := &upstream.Options{TLSConfig: cfg, UnilateralDataHandler: handler}
	var client *upstream.Client
	switch a.IMAPTLS {
	case "implicit":
		secure := tls.Client(conn, cfg)
		if err = secure.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("imap TLS: %w", err)
		}
		conn = secure
		client = upstream.New(conn, options)
	case "starttls":
		client, err = upstream.NewStartTLS(conn, options)
		if err != nil {
			return nil, fmt.Errorf("imap STARTTLS: %w", err)
		}
	default:
		return nil, fmt.Errorf("imap: TLS is required")
	}
	result = &Client{IMAP: client, conn: conn, stop: stop}
	defer func() {
		if err != nil {
			if e := result.Close(); e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	if err = client.WaitGreeting(); err != nil {
		return result, fmt.Errorf("imap greeting: %w", err)
	}
	var authenticator auth.Authenticator = auth.Password{}
	if a.AuthType == "oauth2" {
		authenticator = auth.XOAUTH2{}
	}
	if err = authenticator.Authenticate(ctx, client, a.Email, credentials); err != nil {
		return result, err
	}
	if adapter.NeedsID(a.Provider) {
		if _, err = client.ID(&imap.IDData{Name: "MeldMail", Version: "1.0", Vendor: "MeldMail"}).Wait(); err != nil {
			return result, fmt.Errorf("imap provider ID: %w", err)
		}
	}
	return result, nil
}
func (c *Client) Close() error {
	c.stop()
	if err := c.IMAP.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("imap close: %w", err)
	}
	return nil
}

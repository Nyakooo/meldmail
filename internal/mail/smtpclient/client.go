package smtpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/storage"
	"github.com/wneessen/go-mail/smtp"
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}
type Client struct {
	SMTP *smtp.Client
	conn net.Conn
	stop func() bool
}

func Open(ctx context.Context, a storage.Account, credentials auth.Credentials, d Dialer, tlsConfig *tls.Config) (result *Client, err error) {
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(a.SMTPHost, strconv.Itoa(a.SMTPPort)))
	if err != nil {
		return nil, fmt.Errorf("smtp dial: %w", err)
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
				err = errors.Join(err, fmt.Errorf("smtp cleanup: %w", e))
			}
		}
	}()
	cfg := &tls.Config{ServerName: a.SMTPHost, MinVersion: tls.VersionTLS12}
	if tlsConfig != nil {
		cfg = tlsConfig.Clone()
		cfg.ServerName = a.SMTPHost
	}
	if a.SMTPTLS == "implicit" {
		secure := tls.Client(conn, cfg)
		if err = secure.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("smtp TLS: %w", err)
		}
		conn = secure
	} else if a.SMTPTLS != "starttls" {
		return nil, fmt.Errorf("smtp: TLS is required")
	}
	client, err := smtp.NewClient(conn, a.SMTPHost)
	if err != nil {
		return nil, fmt.Errorf("smtp greeting: %w", err)
	}
	result = &Client{SMTP: client, conn: conn, stop: stop}
	if err = client.Hello("meldmail.local"); err != nil {
		return result, fmt.Errorf("smtp EHLO: %w", err)
	}
	if a.SMTPTLS == "starttls" {
		if err = client.StartTLS(cfg); err != nil {
			return result, fmt.Errorf("smtp STARTTLS: %w", err)
		}
	}
	var authenticator smtp.Auth
	if a.AuthType == "oauth2" {
		authenticator = smtp.XOAuth2Auth(a.Email, credentials.AccessToken)
	} else {
		ok, mechanisms := client.Extension("AUTH")
		if !ok {
			return result, fmt.Errorf("smtp: server does not advertise authentication")
		}
		switch {
		case strings.Contains(" "+mechanisms+" ", " PLAIN "):
			authenticator = smtp.PlainAuth("", a.Email, credentials.Password, a.SMTPHost, false)
		case strings.Contains(" "+mechanisms+" ", " LOGIN "):
			authenticator = smtp.LoginAuth(a.Email, credentials.Password, a.SMTPHost, false)
		default:
			return result, fmt.Errorf("smtp: server does not support PLAIN or LOGIN")
		}
	}
	if err = client.Auth(authenticator); err != nil {
		return result, fmt.Errorf("smtp authentication: %w", err)
	}
	return result, nil
}
func (c *Client) Close() error {
	c.stop()
	if err := c.SMTP.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("smtp close: %w", err)
	}
	return nil
}

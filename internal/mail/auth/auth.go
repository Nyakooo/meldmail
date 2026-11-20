package auth

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2/imapclient"
)

type Credentials struct {
	Password     string `json:"password,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// TokenRefresher is the extension point for provider OAuth2 integration.
type TokenRefresher interface {
	Refresh(context.Context, string) (Credentials, error)
}
type Authenticator interface {
	Authenticate(context.Context, *imapclient.Client, string, Credentials) error
}
type Password struct{}

func (Password) Authenticate(ctx context.Context, c *imapclient.Client, username string, credentials Credentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.Login(username, credentials.Password).Wait(); err != nil {
		return fmt.Errorf("imap password authentication: %w", err)
	}
	return nil
}

type XOAUTH2 struct{ Refresher TokenRefresher }

func (a XOAUTH2) Authenticate(ctx context.Context, c *imapclient.Client, username string, credentials Credentials) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if credentials.AccessToken == "" && a.Refresher != nil {
		updated, err := a.Refresher.Refresh(ctx, credentials.RefreshToken)
		if err != nil {
			return fmt.Errorf("oauth refresh: %w", err)
		}
		credentials = updated
	}
	if credentials.AccessToken == "" {
		return fmt.Errorf("oauth: access token required")
	}
	if err := c.Authenticate(&oauthSASL{user: username, token: credentials.AccessToken}); err != nil {
		return fmt.Errorf("imap OAuth2 authentication: %w", err)
	}
	return nil
}

type oauthSASL struct{ user, token string }

func (a *oauthSASL) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"), nil
}
func (a *oauthSASL) Next(challenge []byte) ([]byte, error) {
	return nil, fmt.Errorf("oauth: server rejected authentication")
}

package imapclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/emersion/go-imap/v2"
	upstream "github.com/emersion/go-imap/v2/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/adapter"
	"github.com/nyakooo/meldmail/internal/mail/auth"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
)

func Watch(ctx context.Context, a storage.Account, credentials auth.Credentials, d Dialer, tlsConfig *tls.Config, notify func()) (result error) {
	handler := &upstream.UnilateralDataHandler{
		Mailbox: func(data *upstream.UnilateralDataMailbox) {
			if data.NumMessages != nil {
				notify()
			}
		},
		Expunge: func(seqNum uint32) { notify() },
		Fetch: func(message *upstream.FetchMessageData) {
			for item := message.Next(); item != nil; item = message.Next() {
				if section, ok := item.(upstream.FetchItemDataBodySection); ok && section.Literal != nil {
					if _, err := io.Copy(io.Discard, section.Literal); err != nil {
						return
					}
				}
			}
			notify()
		},
	}
	c, err := open(ctx, a, credentials, d, tlsConfig, handler)
	if err != nil {
		return err
	}
	defer func() {
		if err := c.Close(); err != nil && ctx.Err() == nil {
			if result == syncer.ErrNoIdle {
				slog.WarnContext(ctx, "mail closed without IDLE", "account_id", a.ID)
				return
			}
			result = errors.Join(result, err)
		}
	}()
	if !c.IMAP.Caps().Has(imap.CapIdle) {
		return syncer.ErrNoIdle
	}
	if _, err := c.IMAP.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("imap IDLE select: %w", err)
	}
	return c.watch(ctx, adapter.IdleInterval(a.Provider), notify)
}
func (c *Client) watch(ctx context.Context, interval time.Duration, notify func()) error {
	health := time.NewTicker(time.Second)
	defer health.Stop()
	for {
		idle, err := c.IMAP.Idle()
		if err != nil {
			return fmt.Errorf("imap IDLE: %w", err)
		}
		slog.InfoContext(ctx, "mail IDLE entered", "account_id", c.accountID, "renew_after_seconds", int64(interval.Seconds()))
		notify()
		timer := time.NewTimer(interval)
		var cause error
	waiting:
		for {
			select {
			case <-ctx.Done():
				cause = ctx.Err()
				break waiting
			case <-timer.C:
				slog.InfoContext(ctx, "mail IDLE renewal", "account_id", c.accountID)
				break waiting
			case <-health.C:
				if c.IMAP.State() == imap.ConnStateLogout {
					cause = fmt.Errorf("imap: IDLE connection lost")
					break waiting
				}
			}
		}
		timer.Stop()
		// Bound a silent server's DONE response without retaining another goroutine.
		timeout := time.AfterFunc(15*time.Second, func() {
			if err := c.conn.Close(); err != nil {
				return
			}
		})
		closeErr := idle.Close()
		waitErr := idle.Wait()
		timeout.Stop()
		if cause != nil {
			return cause
		}
		if closeErr != nil {
			return fmt.Errorf("imap stop IDLE: %w", closeErr)
		}
		if waitErr != nil {
			return fmt.Errorf("imap IDLE completion: %w", waitErr)
		}
	}
}

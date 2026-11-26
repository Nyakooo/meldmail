package mail

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyakooo/meldmail/internal/logging"
	"github.com/nyakooo/meldmail/internal/mail/imapclient"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
	"github.com/nyakooo/meldmail/internal/storage"
)

func (s *Service) AccountsChanged() {
	if s.Workers != nil {
		s.Workers.Changed()
	}
}
func (s *Service) NewWorkers(options syncer.Options) *syncer.Manager {
	return syncer.NewManager(syncer.Backend{Accounts: s.workerAccounts, Sync: s.Sync, Watch: s.watchAccount, Reconcile: s.reconcileAccount, Status: s.workerStatus}, options)
}
func (s *Service) workerAccounts(ctx context.Context) ([]syncer.Account, error) {
	accounts, err := s.Store.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	proxy := ""
	for _, account := range accounts {
		if account.ProxyMode == "global" {
			proxy, err = s.Store.Setting(ctx, "global_proxy_enc")
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			break
		}
	}
	result := make([]syncer.Account, 0, len(accounts))
	for _, a := range accounts {
		// Shared proxy edits must not restart direct or mailbox-specific connections.
		shared := ""
		if a.ProxyMode == "global" {
			shared = proxy
		}
		// Only connection settings participate, so sync status/timestamps cannot restart workers.
		config := struct {
			Email, Provider, AuthType, IMAPHost, IMAPTLS, ProxyMode, Global string
			IMAPPort                                                        int
			Credentials, Proxy                                              []byte
		}{a.Email, a.Provider, a.AuthType, a.IMAPHost, a.IMAPTLS, a.ProxyMode, shared, a.IMAPPort, a.CredentialsEnc, a.ProxyURLEnc}
		encoded, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("worker configuration: %w", err)
		}
		digest := sha256.Sum256(encoded)
		result = append(result, syncer.Account{ID: a.ID, Revision: hex.EncodeToString(digest[:]), Paused: a.Status == "paused"})
	}
	return result, nil
}
func (s *Service) watchAccount(ctx context.Context, id int64, notify func()) (result error) {
	started := time.Now()
	slog.InfoContext(ctx, "mail operation started", "account_id", id, "operation", "watch")
	defer func() { logMailOperation(ctx, id, "watch", started, result, -1) }()
	a, err := s.Store.Account(ctx, id)
	if err != nil {
		return err
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		return err
	}
	d, err := s.Dialer(ctx, a)
	if err != nil {
		return err
	}
	return imapclient.Watch(ctx, a, credentials, d, s.TLSConfig, notify)
}
func (s *Service) reconcileAccount(ctx context.Context, id int64) (result error) {
	started := time.Now()
	defer func() { logMailOperation(ctx, id, "reconcile", started, result, -1) }()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	release, err := s.Limiter.Acquire(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	a, err := s.Store.Account(ctx, id)
	if err != nil {
		return err
	}
	credentials, err := s.Credentials(a)
	if err != nil {
		return err
	}
	d, err := s.Dialer(ctx, a)
	if err != nil {
		return err
	}
	c, err := imapclient.Open(ctx, a, credentials, d, s.TLSConfig)
	if err != nil {
		return err
	}
	defer func() {
		if err := c.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	return c.Reconcile(ctx, s.Store, a, func(flags storage.EmailFlags) error { return s.Events.Publish("flag_updated", flags) })
}
func (s *Service) workerStatus(ctx context.Context, id int64, mode string, cause error) error {
	attrs := []slog.Attr{slog.Int64("account_id", id), slog.String("mode", mode)}
	attrs = append(attrs, logging.Failure(cause)...)
	level := slog.LevelInfo
	if cause != nil {
		level = slog.LevelWarn
	}
	slog.Default().LogAttrs(ctx, level, "mail worker status", attrs...)
	status := "active"
	diagnostic := ""
	if cause != nil {
		status = "error"
		diagnostic = diagnose(cause)
	}
	if err := s.Store.SetAccountStatus(ctx, id, status, diagnostic); err != nil {
		return err
	}
	return s.Events.Publish("sync_status", map[string]interface{}{"account_id": id, "status": status, "mode": mode, "last_error": diagnostic})
}

func logMailOperation(ctx context.Context, id int64, operation string, started time.Time, cause error, count int) {
	attrs := []slog.Attr{slog.Int64("account_id", id), slog.String("operation", operation), slog.Int64("duration_ms", time.Since(started).Milliseconds())}
	if count >= 0 {
		attrs = append(attrs, slog.Int("messages", count))
	}
	outcome := "success"
	level := slog.LevelInfo
	if cause != nil {
		outcome = "failed"
		level = slog.LevelWarn
		if errors.Is(cause, context.Canceled) && ctx.Err() == context.Canceled {
			outcome = "canceled"
			level = slog.LevelInfo
		}
		attrs = append(attrs, logging.Failure(cause)...)
	}
	attrs = append(attrs, slog.String("outcome", outcome))
	slog.Default().LogAttrs(ctx, level, "mail operation completed", attrs...)
}

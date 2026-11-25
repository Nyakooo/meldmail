package mail

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/nyakooo/meldmail/internal/proxy"
)

// ConfigureHostProxy runs before workers start. Environment settings own only
// the shared proxy; direct and independent mailbox settings remain independent.
func (s *Service) ConfigureHostProxy(ctx context.Context, endpoint string) (result error) {
	if _, err := proxy.New("global", "", endpoint); err != nil {
		return err
	}
	managed, err := s.Store.Setting(ctx, "host_proxy_managed")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("host proxy read ownership: %w", err)
	}
	if endpoint == "" && managed != "1" {
		return nil
	}
	// Read the persisted value even if this service was configured previously.
	s.HostProxyURL = ""
	current, readErr := s.GlobalProxy(ctx)
	if endpoint != "" && endpoint == current && readErr == nil && managed == "1" {
		s.HostProxyURL = endpoint
		return nil
	}
	value := ""
	if endpoint != "" {
		encrypted, err := s.Cipher.Encrypt([]byte(endpoint))
		if err != nil {
			return fmt.Errorf("host proxy encrypt: %w", err)
		}
		value = base64.StdEncoding.EncodeToString(encrypted)
	}
	tx, err := s.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("host proxy begin: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			result = errors.Join(result, fmt.Errorf("host proxy rollback: %w", err))
		}
	}()
	owner := ""
	if endpoint != "" {
		owner = "1"
	}
	for key, entry := range map[string]string{"global_proxy_enc": value, "host_proxy_managed": owner} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, entry, time.Now().Unix()); err != nil {
			return fmt.Errorf("host proxy update: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("host proxy commit: %w", err)
	}
	s.HostProxyURL = endpoint
	return nil
}

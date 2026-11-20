package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Account struct {
	ID             int64  `json:"id"`
	Email          string `json:"email"`
	DisplayName    string `json:"display_name"`
	Provider       string `json:"provider"`
	AuthType       string `json:"auth_type"`
	IMAPHost       string `json:"imap_host"`
	IMAPPort       int    `json:"imap_port"`
	IMAPTLS        string `json:"imap_tls"`
	SMTPHost       string `json:"smtp_host"`
	SMTPPort       int    `json:"smtp_port"`
	SMTPTLS        string `json:"smtp_tls"`
	CredentialsEnc []byte `json:"-"`
	ProxyMode      string `json:"proxy_mode"`
	ProxyURLEnc    []byte `json:"-"`
	Status         string `json:"status"`
	LastError      string `json:"last_error"`
	LastSyncAt     int64  `json:"last_sync_at"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

const accountColumns = `id,email,display_name,provider,auth_type,imap_host,imap_port,imap_tls,smtp_host,smtp_port,smtp_tls,credentials_enc,proxy_mode,proxy_url_enc,status,last_error,last_sync_at,created_at,updated_at`

type scanner interface{ Scan(...interface{}) error }

func scanAccount(row scanner) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Email, &a.DisplayName, &a.Provider, &a.AuthType, &a.IMAPHost, &a.IMAPPort, &a.IMAPTLS, &a.SMTPHost, &a.SMTPPort, &a.SMTPTLS, &a.CredentialsEnc, &a.ProxyMode, &a.ProxyURLEnc, &a.Status, &a.LastError, &a.LastSyncAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Account{}, fmt.Errorf("storage account: %w", err)
	}
	return a, nil
}
func (s *Store) Accounts(ctx context.Context) (accounts []Account, resultErr error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+accountColumns+" FROM accounts ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("storage accounts: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("storage close account rows: %w", err))
		}
	}()
	result := make([]Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage account rows: %w", err)
	}
	return result, nil
}
func (s *Store) Account(ctx context.Context, id int64) (Account, error) {
	return scanAccount(s.DB.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE id=?", id))
}
func (s *Store) SaveAccount(ctx context.Context, a Account) (Account, error) {
	now := time.Now().Unix()
	if a.ID == 0 {
		result, err := s.DB.ExecContext(ctx, `INSERT INTO accounts(email,display_name,provider,auth_type,imap_host,imap_port,imap_tls,smtp_host,smtp_port,smtp_tls,credentials_enc,proxy_mode,proxy_url_enc,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, a.Email, a.DisplayName, a.Provider, a.AuthType, a.IMAPHost, a.IMAPPort, a.IMAPTLS, a.SMTPHost, a.SMTPPort, a.SMTPTLS, a.CredentialsEnc, a.ProxyMode, a.ProxyURLEnc, a.Status, now, now)
		if err != nil {
			return Account{}, fmt.Errorf("storage insert account: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return Account{}, fmt.Errorf("storage account ID: %w", err)
		}
		a.ID = id
	} else {
		result, err := s.DB.ExecContext(ctx, `UPDATE accounts SET email=?,display_name=?,provider=?,auth_type=?,imap_host=?,imap_port=?,imap_tls=?,smtp_host=?,smtp_port=?,smtp_tls=?,credentials_enc=?,proxy_mode=?,proxy_url_enc=?,status=?,last_error='',updated_at=? WHERE id=?`, a.Email, a.DisplayName, a.Provider, a.AuthType, a.IMAPHost, a.IMAPPort, a.IMAPTLS, a.SMTPHost, a.SMTPPort, a.SMTPTLS, a.CredentialsEnc, a.ProxyMode, a.ProxyURLEnc, a.Status, now, a.ID)
		if err != nil {
			return Account{}, fmt.Errorf("storage update account: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return Account{}, fmt.Errorf("storage affected account: %w", err)
		}
		if n == 0 {
			return Account{}, fmt.Errorf("storage update account: %w", sql.ErrNoRows)
		}
	}
	return s.Account(ctx, a.ID)
}
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM accounts WHERE id=?", id)
	if err != nil {
		return fmt.Errorf("storage delete account: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage affected account: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("storage delete account: %w", sql.ErrNoRows)
	}
	return nil
}
func (s *Store) PutSetting(ctx context.Context, key, value string) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, time.Now().Unix()); err != nil {
		return fmt.Errorf("storage update setting: %w", err)
	}
	return nil
}
func (s *Store) SetAccountStatus(ctx context.Context, id int64, status, diagnostic string) error {
	if _, err := s.DB.ExecContext(ctx, "UPDATE accounts SET status=?,last_error=?,updated_at=? WHERE id=?", status, diagnostic, time.Now().Unix(), id); err != nil {
		return fmt.Errorf("storage account status: %w", err)
	}
	return nil
}

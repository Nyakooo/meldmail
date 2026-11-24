package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nyakooo/meldmail/migrations"
	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("storage directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("storage open: %w", err)
	}
	// One persistent connection keeps every operation under the same PRAGMA policy.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fail := func(cause error) (*Store, error) {
		if e := db.Close(); e != nil {
			return nil, fmt.Errorf("storage close after %v: %w", cause, e)
		}
		return nil, cause
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA cache_size=-16000", "PRAGMA temp_store=MEMORY", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", migrations.Initial, migrations.EmailBodies} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fail(fmt.Errorf("storage initialize: %w", err))
		}
	}
	return &Store{db}, nil
}
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	if err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", key).Scan(&v); err != nil {
		return "", fmt.Errorf("storage setting: %w", err)
	}
	return v, nil
}
func (s *Store) InitPassword(ctx context.Context, hash string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO settings(key,value,updated_at) VALUES('admin_password',?,?) ON CONFLICT(key) DO NOTHING", hash, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("storage initialize password: %w", err)
	}
	return nil
}

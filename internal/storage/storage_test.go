package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInitialization(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.DB.Close(); err != nil {
			t.Error(err)
		}
	}()
	var mode string
	if err := s.DB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal=%s", mode)
	}
	for q, want := range map[string]int{"PRAGMA foreign_keys": 1, "PRAGMA cache_size": -16000, "PRAGMA temp_store": 2, "PRAGMA busy_timeout": 5000, "PRAGMA synchronous": 1} {
		var got int
		if err := s.DB.QueryRowContext(ctx, q).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d", q, got)
		}
	}
	if err := s.InitPassword(ctx, "hash-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.InitPassword(ctx, "hash-two"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Setting(ctx, "admin_password")
	if err != nil || got != "hash-one" {
		t.Fatal("password overwritten", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name IN ('accounts','folders','emails','emails_fts','mcp_tokens','pending_actions','audit_logs','settings')").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Fatalf("missing schema: %d", n)
	}
}

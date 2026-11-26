package logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDailyRotationRetentionAndConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 26, 23, 59, 0, 0, time.UTC)
	for _, name := range []string{"meldmail-2026-11-19.jsonl", "meldmail-2026-11-20.jsonl", "notes.txt", "meldmail-invalid.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("preserve\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Construct with a controlled clock so the host date does not affect retention.
	f := &Files{dir: dir, now: func() time.Time { return now }}
	if _, err := f.Write([]byte("before midnight\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meldmail-2026-11-19.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expired file retained: %v", err)
	}
	for _, name := range []string{"meldmail-2026-11-20.jsonl", "notes.txt", "meldmail-invalid.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := fmt.Fprintln(f, "after midnight"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meldmail-2026-11-20.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("rollover did not expire file: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "meldmail-2026-11-27.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 20*len("after midnight\n") {
		t.Fatalf("lost concurrent records: %d bytes", len(data))
	}
	info, err := os.Stat(filepath.Join(dir, "meldmail-2026-11-27.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("log permissions: %v", info.Mode())
	}
}

func TestOpenAppendsAcrossRestartAndStopsMaintenance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	for i := 0; i < 2; i++ {
		f, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintln(f, "record"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := f.Maintain(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(names) != 1 {
		t.Fatalf("daily files: %v %v", names, err)
	}
	data, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "record\nrecord\n" {
		t.Fatalf("restart overwrote logs: %q", data)
	}
}

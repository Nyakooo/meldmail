// Package logging provides bounded-retention diagnostic files without external services.
package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Files writes daily UTC files and retains today plus the preceding six days.
// Only files matching meldmail-YYYY-MM-DD.jsonl are eligible for deletion.
type Files struct {
	mu   sync.Mutex
	dir  string
	file *os.File
	day  string
	now  func() time.Time
}

func Open(dir string) (*Files, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("logging directory: %w", err)
	}
	f := &Files{dir: dir, now: time.Now}
	if err := f.rotate(f.now().UTC()); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *Files) rotate(now time.Time) error {
	day := now.Format(time.DateOnly)
	if f.file != nil && f.day == day {
		return nil
	}
	if err := f.cleanup(now); err != nil {
		return err
	}
	next, err := os.OpenFile(filepath.Join(f.dir, "meldmail-"+day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("logging open: %w", err)
	}
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return errors.Join(fmt.Errorf("logging close: %w", err), next.Close())
		}
	}
	f.file, f.day = next, day
	return nil
}
func (f *Files) cleanup(now time.Time) error {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return fmt.Errorf("logging list: %w", err)
	}
	cutoff := now.AddDate(0, 0, -6).Format(time.DateOnly)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, "meldmail-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "meldmail-"), ".jsonl")
		if _, err := time.Parse(time.DateOnly, day); err != nil {
			continue
		}
		if day < cutoff {
			if err := os.Remove(filepath.Join(f.dir, name)); err != nil {
				return fmt.Errorf("logging remove expired: %w", err)
			}
		}
	}
	return nil
}
func (f *Files) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.rotate(f.now().UTC()); err != nil {
		return 0, err
	}
	return f.file.Write(p)
}
func (f *Files) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	if err != nil {
		return fmt.Errorf("logging close: %w", err)
	}
	return nil
}

// Maintain also expires old files when no new records are being written.
func (f *Files) Maintain(ctx context.Context) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			f.mu.Lock()
			err := f.rotate(f.now().UTC())
			f.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

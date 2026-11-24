package observe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotDataFilesAndSampling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.db")
	for suffix, size := range map[string]int{"": 1024, "-wal": 128, "-shm": 32} {
		if err := os.WriteFile(path+suffix, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
	}
	collector := New()
	first := collector.Snapshot(path)
	if first.Memory.Used == 0 || !first.Disk.Available || first.Disk.Total != 1184 {
		t.Fatalf("invalid resource snapshot: %+v", first)
	}
	collector.mu.Lock()
	collector.previous = time.Now().Add(-2 * time.Second)
	collector.mu.Unlock()
	second := collector.Snapshot(path)
	if second.CPU.Source != "unavailable" && (second.CPU.Percent == nil || *second.CPU.Percent < 0) {
		t.Fatal("CPU second sample unavailable")
	}
	missing := collector.Snapshot(filepath.Join(t.TempDir(), "missing.db"))
	if missing.Disk.Available {
		t.Fatal("missing database reported as available")
	}
}

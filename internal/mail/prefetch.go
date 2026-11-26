package mail

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nyakooo/meldmail/internal/logging"
	"github.com/nyakooo/meldmail/internal/mail/syncer"
)

func (s *Service) runBodyPrefetch(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.prefetchBodies(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.prefetchWake:
		case <-ticker.C:
		}
	}
}

func (s *Service) prefetchBodies(ctx context.Context) {
	if !bodyPrefetchMemoryAvailable() {
		return
	}
	ids, err := s.Store.BodyPrefetchCandidates(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Default().LogAttrs(ctx, slog.LevelWarn, "mail prefetch candidates failed", logging.Failure(err)...)
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil || !bodyPrefetchMemoryAvailable() || !s.prefetchBody(ctx, id) {
			return
		}
	}
}

func (s *Service) prefetchBody(ctx context.Context, id int64) bool {
	s.prefetchMu.Lock()
	if s.activeReaders > 0 {
		s.prefetchMu.Unlock()
		return false
	}
	select {
	case s.bodyMemory <- struct{}{}:
	default:
		s.prefetchMu.Unlock()
		return false
	}
	itemCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	s.cancelPrefetch = cancel
	s.prefetchMu.Unlock()
	defer func() {
		cancel()
		s.prefetchMu.Lock()
		s.cancelPrefetch = nil
		<-s.bodyMemory
		s.prefetchMu.Unlock()
	}()

	content, err := s.readBody(itemCtx, id, true)
	if itemCtx.Err() == context.Canceled || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, syncer.ErrBusy) {
		return true
	}
	if err != nil || content.CacheWarning != "" {
		slog.Default().LogAttrs(ctx, slog.LevelDebug, "mail body prefetch failed", logging.Failure(err)...)
	} else if content.Cached && !content.Local {
		slog.DebugContext(ctx, "mail body prefetched")
	}
	return true
}

func bodyPrefetchMemoryAvailable() bool {
	const budget = 256 << 20
	for _, files := range [][2]string{
		{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory.current"},
		{"/sys/fs/cgroup/memory/memory.limit_in_bytes", "/sys/fs/cgroup/memory/memory.usage_in_bytes"},
	} {
		limit, ok := readMemoryBytes(files[0])
		if ok && limit > 0 && limit < 1<<60 {
			used, ok := readMemoryBytes(files[1])
			return ok && used < min(limit, budget)/2
		}
	}
	// Standalone installs may have no finite cgroup limit. Use process RSS on Linux.
	if stat, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(stat))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return pages < budget/2/uint64(os.Getpagesize())
			}
		}
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys-stats.HeapReleased < budget/2
}

func readMemoryBytes(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return value, err == nil
}

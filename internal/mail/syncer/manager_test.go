package syncer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testOptions() Options {
	options := DefaultOptions()
	options.RefreshInterval = time.Hour
	options.ReconcileInterval = time.Hour
	options.StableInterval = time.Hour
	options.PollInterval = 100 * time.Millisecond
	options.Backoff = []time.Duration{5 * time.Millisecond, 15 * time.Millisecond, 30 * time.Millisecond, 60 * time.Millisecond}
	return options
}
func TestRetryPollingAndShutdown(t *testing.T) {
	options := testOptions()
	modes := make(chan string, 10)
	var watching atomic.Int64
	backend := Backend{
		Accounts: func(context.Context) ([]Account, error) { return []Account{{ID: 1}}, nil },
		Sync:     func(context.Context, int64) (int, error) { return 0, nil },
		Watch: func(context.Context, int64, func()) error {
			watching.Add(1)
			defer watching.Add(-1)
			return errors.New("offline")
		},
		Reconcile: func(context.Context, int64) error { return nil },
		Status: func(ctx context.Context, id int64, mode string, cause error) error {
			if cause != nil {
				modes <- mode
			}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	manager := NewManager(backend, options)
	go func() { done <- manager.Run(ctx) }()
	for i := 0; i < 5; i++ {
		select {
		case mode := <-modes:
			want := "reconnecting"
			if i == 4 {
				want = "polling"
			}
			if mode != want {
				t.Fatalf("failure %d mode=%s", i+1, mode)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not retry")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if watching.Load() != 0 {
		t.Fatal("watch goroutine leaked")
	}
	defaults := DefaultOptions()
	if defaults.PollInterval != 3*time.Minute || defaults.ReconcileInterval != 45*time.Minute || len(defaults.Backoff) != 4 || defaults.Backoff[0] != 5*time.Second || defaults.Backoff[1] != 15*time.Second || defaults.Backoff[2] != 30*time.Second || defaults.Backoff[3] != 60*time.Second {
		t.Fatal("production timings violate specification")
	}
}
func TestNotificationsReconcileAndAccountChanges(t *testing.T) {
	var mu sync.Mutex
	accounts := []Account{{ID: 1, Revision: "first"}}
	started := make(chan func(), 4)
	synced := make(chan struct{}, 20)
	reconciled := make(chan struct{}, 4)
	var watching atomic.Int64
	backend := Backend{
		Accounts: func(context.Context) ([]Account, error) {
			mu.Lock()
			defer mu.Unlock()
			return append([]Account(nil), accounts...), nil
		},
		Sync: func(context.Context, int64) (int, error) { synced <- struct{}{}; return 1, nil },
		Watch: func(ctx context.Context, id int64, notify func()) error {
			watching.Add(1)
			defer watching.Add(-1)
			started <- notify
			<-ctx.Done()
			return ctx.Err()
		},
		Reconcile: func(context.Context, int64) error {
			select {
			case reconciled <- struct{}{}:
			default:
			}
			return nil
		},
		Status: func(context.Context, int64, string, error) error { return nil },
	}
	options := testOptions()
	options.ReconcileInterval = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	manager := NewManager(backend, options)
	go func() { done <- manager.Run(ctx) }()
	var notify func()
	select {
	case notify = <-started:
	case <-time.After(time.Second):
		t.Fatal("no background watcher")
	}
	<-synced
	notify()
	select {
	case <-synced:
	case <-time.After(time.Second):
		t.Fatal("notification did not sync")
	}
	select {
	case <-reconciled:
	case <-time.After(time.Second):
		t.Fatal("periodic flag reconciliation missing")
	}
	mu.Lock()
	accounts[0].Revision = "second"
	mu.Unlock()
	manager.Changed()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("config change did not restart worker")
	}
	if watching.Load() != 1 {
		t.Fatal("old watcher retained")
	}
	mu.Lock()
	accounts[0].Paused = true
	mu.Unlock()
	manager.Changed()
	deadline := time.Now().Add(time.Second)
	for watching.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if watching.Load() != 0 {
		t.Fatal("paused watcher retained")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestGlobalSyncLimit(t *testing.T) {
	limiter := NewLimiter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var active, peak atomic.Int64
	var wg sync.WaitGroup
	for i := int64(1); i <= 10; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			release, err := limiter.Acquire(ctx, id)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			n := active.Add(1)
			for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
		}(i)
	}
	wg.Wait()
	if peak.Load() != 3 || active.Load() != 0 {
		t.Fatalf("peak=%d active=%d", peak.Load(), active.Load())
	}
}

// A transient flag-action failure can mark an otherwise live account as error.
// Successful IDLE-driven synchronization must publish its recovered status.
func TestNotificationPublishesRecoveredStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan func(), 1)
	statuses := make(chan string, 8)
	synced := make(chan struct{}, 8)
	backend := Backend{
		Watch: func(ctx context.Context, id int64, notify func()) error {
			ready <- notify
			<-ctx.Done()
			return ctx.Err()
		},
		Sync:      func(context.Context, int64) (int, error) { synced <- struct{}{}; return 0, nil },
		Status:    func(ctx context.Context, id int64, mode string, err error) error { statuses <- mode; return nil },
		Reconcile: func(context.Context, int64) error { return nil },
	}
	done := make(chan error, 1)
	go func() { done <- NewManager(backend, testOptions()).session(ctx, 1, func() {}) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("session exit: %v", err)
		}
	}()
	notify := <-ready
	<-synced
	<-statuses // Initial healthy status, before a later transient failure.
	notify()
	<-synced
	select {
	case mode := <-statuses:
		if mode != "idle" {
			t.Fatalf("recovered mode = %s", mode)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("successful synchronization left the UI in reconnecting status")
	}
}

func TestPollingStatusWhenIdleUnsupported(t *testing.T) {
	syncFailure := errors.New("sync failed")
	for _, tc := range []struct {
		name    string
		syncErr error
	}{
		{name: "successful sync"},
		{name: "failed sync", syncErr: syncFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var polling bool
			var cause error
			manager := NewManager(Backend{
				Sync:  func(context.Context, int64) (int, error) { return 0, tc.syncErr },
				Watch: func(context.Context, int64, func()) error { return ErrNoIdle },
				Status: func(_ context.Context, _ int64, mode string, err error) error {
					if mode == "polling" {
						polling, cause = true, err
						cancel()
					}
					return nil
				},
			}, testOptions())
			if err := manager.worker(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if !polling || !errors.Is(cause, tc.syncErr) {
				t.Fatalf("polling=%v, cause=%v, want polling with cause %v", polling, cause, tc.syncErr)
			}
		})
	}
}

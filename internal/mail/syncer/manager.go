package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var ErrNoIdle = errors.New("IDLE unavailable")
var ErrBusy = errors.New("account synchronization already running")

type Account struct {
	ID       int64
	Revision string
	Paused   bool
}
type Backend struct {
	Accounts  func(context.Context) ([]Account, error)
	Sync      func(context.Context, int64) (int, error)
	Watch     func(context.Context, int64, func()) error
	Reconcile func(context.Context, int64) error
	Status    func(context.Context, int64, string, error) error
}
type Options struct {
	RefreshInterval, ReconcileInterval, PollInterval, StableInterval time.Duration
	Backoff                                                          []time.Duration
}

func DefaultOptions() Options {
	return Options{RefreshInterval: 30 * time.Second, ReconcileInterval: 45 * time.Minute, PollInterval: 3 * time.Minute, StableInterval: time.Minute, Backoff: []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}}
}

type Manager struct {
	backend Backend
	options Options
	changed chan struct{}
}

func NewManager(backend Backend, options Options) *Manager {
	return &Manager{backend: backend, options: options, changed: make(chan struct{}, 1)}
}
func (m *Manager) Changed() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}

type running struct {
	revision string
	cancel   context.CancelFunc
	done     chan error
}

func (m *Manager) Run(ctx context.Context) (result error) {
	workers := make(map[int64]running)
	stop := func(id int64) error {
		worker := workers[id]
		worker.cancel()
		err := <-worker.done
		delete(workers, id)
		return err
	}
	defer func() {
		if errors.Is(result, context.Canceled) && ctx.Err() != nil {
			result = nil
		}
		for id := range workers {
			if err := stop(id); err != nil {
				result = errors.Join(result, err)
			}
		}
	}()
	refresh := func() error {
		accounts, err := m.backend.Accounts(ctx)
		if err != nil {
			return fmt.Errorf("syncer list accounts: %w", err)
		}
		desired := make(map[int64]Account)
		for _, a := range accounts {
			if !a.Paused {
				desired[a.ID] = a
			}
		}
		for id, worker := range workers {
			a, ok := desired[id]
			if !ok || a.Revision != worker.revision {
				if err := stop(id); err != nil {
					return err
				}
			}
		}
		for id, a := range desired {
			if _, ok := workers[id]; ok {
				continue
			}
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			workers[id] = running{a.Revision, cancel, done}
			go func(id int64) { done <- m.worker(workerCtx, id) }(id)
		}
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	timer := time.NewTicker(m.options.RefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.changed:
			if err := refresh(); err != nil {
				return err
			}
		case <-timer.C:
			for id, worker := range workers {
				select {
				case err := <-worker.done:
					delete(workers, id)
					worker.cancel()
					if err != nil {
						return err
					}
				default:
				}
			}
			if err := refresh(); err != nil {
				return err
			}
		}
	}
}
func (m *Manager) worker(ctx context.Context, id int64) (result error) {
	defer func() {
		if ctx.Err() != nil {
			result = nil
		}
	}()
	failures := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := m.session(ctx, id, func() { failures = 0 })
		if ctx.Err() != nil {
			return nil
		}
		failures++
		delay := m.options.PollInterval
		mode := "reconnecting"
		if failures <= len(m.options.Backoff) && !errors.Is(err, ErrNoIdle) {
			delay = m.options.Backoff[failures-1]
		} else {
			mode = "polling"
		}
		if statusErr := m.backend.Status(ctx, id, mode, err); statusErr != nil {
			return statusErr
		}
		slog.InfoContext(ctx, "mail retry scheduled", "account_id", id, "mode", mode, "failures", failures, "retry_after_ms", delay.Milliseconds())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
func (m *Manager) session(ctx context.Context, id int64, onStable func()) (result error) {
	watchCtx, cancel := context.WithCancel(ctx)
	wake := make(chan struct{}, 1)
	done := make(chan error, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	go func() { done <- m.backend.Watch(watchCtx, id, notify) }()
	consumed := false
	defer func() {
		cancel()
		if !consumed {
			if err := <-done; err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				result = errors.Join(result, err)
			}
		}
	}()
	if _, err := m.backend.Sync(ctx, id); err != nil && !errors.Is(err, ErrBusy) {
		return err
	}
	if err := m.backend.Status(ctx, id, "idle", nil); err != nil {
		return err
	}
	var stableTimer *time.Timer
	var stable <-chan time.Time
	defer func() {
		if stableTimer != nil {
			stableTimer.Stop()
		}
	}()
	ticker := time.NewTicker(m.options.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			consumed = true
			if err == nil {
				err = fmt.Errorf("syncer: IDLE ended")
			}
			return err
		case <-stable:
			onStable()
			stable = nil
		case <-wake:
			if stableTimer == nil {
				stableTimer = time.NewTimer(m.options.StableInterval)
				stable = stableTimer.C
			}
			if _, err := m.backend.Sync(ctx, id); err != nil {
				if !errors.Is(err, ErrBusy) {
					return err
				}
			} else if err := m.backend.Status(ctx, id, "idle", nil); err != nil {
				return err
			}
		case <-ticker.C:
			if err := m.backend.Reconcile(ctx, id); err != nil && !errors.Is(err, ErrBusy) {
				return err
			}
			if _, err := m.backend.Sync(ctx, id); err != nil {
				if !errors.Is(err, ErrBusy) {
					return err
				}
			} else if err := m.backend.Status(ctx, id, "idle", nil); err != nil {
				return err
			}
		}
	}
}

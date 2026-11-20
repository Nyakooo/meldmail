package syncer

import (
	"context"
	"fmt"
	"sync"
)

// Limiter is shared by every sync path; IDLE waiting itself does not hold a slot.
type Limiter struct {
	slots  chan struct{}
	mu     sync.Mutex
	active map[int64]bool
}

func NewLimiter() *Limiter {
	return &Limiter{slots: make(chan struct{}, 3), active: make(map[int64]bool)}
}
func (l *Limiter) Acquire(ctx context.Context, id int64) (func(), error) {
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	l.mu.Lock()
	if l.active[id] {
		l.mu.Unlock()
		<-l.slots
		return nil, fmt.Errorf("sync: account is already synchronizing")
	}
	l.active[id] = true
	l.mu.Unlock()
	return func() { l.mu.Lock(); delete(l.active, id); l.mu.Unlock(); <-l.slots }, nil
}

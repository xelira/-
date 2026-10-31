// Package tcpbackoff rate-limits direct TCP fallback attempts to Telegram.
//
// When the direct path to a DC is blocked every attempt costs a full dial
// timeout. Like Flowseal/tg-ws-proxy (TCP_BACKOFF_*), a failed target is not
// retried for 30s, doubling per consecutive failure up to one hour, and only
// one dial per target runs at a time.
package tcpbackoff

import (
	"sync"
	"time"
)

const (
	DefaultInitial = 30 * time.Second
	DefaultMax     = time.Hour
	maxDoublings   = 7
)

type Tracker struct {
	mu         sync.Mutex
	initial    time.Duration
	max        time.Duration
	now        func() time.Time
	failures   map[string]int
	retryAfter map[string]time.Time
	connecting map[string]bool
}

func New() *Tracker {
	return &Tracker{
		initial:    DefaultInitial,
		max:        DefaultMax,
		now:        time.Now,
		failures:   make(map[string]int),
		retryAfter: make(map[string]time.Time),
		connecting: make(map[string]bool),
	}
}

// Begin reserves the right to dial key. It returns false while another dial to
// the same key is in flight or the key is still backing off.
func (t *Tracker) Begin(key string) bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connecting[key] {
		return false
	}
	if until, ok := t.retryAfter[key]; ok && t.now().Before(until) {
		return false
	}
	t.connecting[key] = true
	return true
}

// End releases the reservation taken by Begin. A non-nil err extends the
// backoff and returns the delay now applied; success resets it.
func (t *Tracker) End(key string, err error) time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.connecting, key)
	if err == nil {
		delete(t.failures, key)
		delete(t.retryAfter, key)
		return 0
	}
	failures := t.failures[key] + 1
	t.failures[key] = failures
	n := failures - 1
	if n > maxDoublings {
		n = maxDoublings
	}
	delay := t.initial << uint(n)
	if delay > t.max || delay <= 0 {
		delay = t.max
	}
	t.retryAfter[key] = t.now().Add(delay)
	return delay
}

// Reset forgets all state (used when the proxy is restarted).
func (t *Tracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.failures = make(map[string]int)
	t.retryAfter = make(map[string]time.Time)
	t.connecting = make(map[string]bool)
	t.mu.Unlock()
}

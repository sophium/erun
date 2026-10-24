// Package ratelimit is a fixed-window request limiter keyed by an arbitrary string.
package ratelimit

import (
	"sync"
	"time"
)

// Result reports one admission decision.
type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
	ResetAt    time.Time
}

type bucket struct {
	count   int
	resetAt time.Time
}

// Limiter is a fixed-window counter per key. Expired windows are replaced
// lazily; there is no background sweep.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]bucket
}

// NewLimiter returns a Limiter with no buckets yet.
func NewLimiter() *Limiter {
	return &Limiter{buckets: make(map[string]bucket)}
}

// Allow admits or refuses one call under key; now is passed in for determinism.
func (l *Limiter) Allow(key string, limit int, window time.Duration, now time.Time) Result {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok || !now.Before(b.resetAt) {
		b = bucket{resetAt: now.Add(window)}
	}
	b.count++
	l.buckets[key] = b

	remaining := limit - b.count
	if remaining < 0 {
		remaining = 0
	}
	return Result{
		Allowed:    b.count <= limit,
		Limit:      limit,
		Remaining:  remaining,
		RetryAfter: b.resetAt.Sub(now),
		ResetAt:    b.resetAt,
	}
}

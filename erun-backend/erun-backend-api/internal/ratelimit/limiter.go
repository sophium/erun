// Package ratelimit is the first request-rate limiter in erun-backend-api
// (erun issue's §9: "Not implemented yet" in erun-docs/docs/agent-reference/
// api-protocol.md's Rate limits section, until this). It is built as a
// general fixed-window bucket keyed by an arbitrary string, so the general
// per-token/per-tenant limiter that section documents as the target shape
// can adopt this same abstraction later rather than replace it — the first
// caller is POST /v1/invite-requests' two tiers (source address before
// verification, verified (issuer, subject) after), wired in
// internal/routes/invite_requests.go.
package ratelimit

import (
	"sync"
	"time"
)

// Result reports one admission decision.
type Result struct {
	Allowed bool
	// Limit is the bucket's own ceiling, echoed back for the RateLimit-Limit
	// response header.
	Limit int
	// Remaining is how many more calls this window admits, floored at 0.
	Remaining int
	// RetryAfter is how long until a refused caller may retry.
	RetryAfter time.Duration
	// ResetAt is when the current window's count returns to zero.
	ResetAt time.Time
}

type bucket struct {
	count   int
	resetAt time.Time
}

// Limiter is a fixed-window counter per key. Windows are created lazily and
// replaced once their deadline passes, so memory is bounded by the number of
// distinct keys seen inside one still-live window — there is no background
// sweep, matching the bounded-TTL-cache shape already used elsewhere in this
// module (e.g. the identity resolution cache) rather than adding a new
// pattern for a low-traffic endpoint.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]bucket
}

// NewLimiter returns a Limiter with no buckets yet.
func NewLimiter() *Limiter {
	return &Limiter{buckets: make(map[string]bucket)}
}

// Allow admits or refuses one call under key against a fixed window of the
// given size and per-window limit, evaluated at now (passed in rather than
// read from time.Now() so callers can test it deterministically).
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

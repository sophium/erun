package backendapi

import (
	"sync"
	"time"
)

type IdentityCacheOptions struct {
	PositiveTTL time.Duration
	NegativeTTL time.Duration
	Now         func() time.Time
}

type IdentityResolutionCache struct {
	mu          sync.Mutex
	positiveTTL time.Duration
	negativeTTL time.Duration
	now         func() time.Time
	entries     map[identityCacheKey]identityCacheEntry
}

type identityCacheKey struct {
	issuer  string
	subject string
}

type identityCacheEntry struct {
	identity  Identity
	err       error
	expiresAt time.Time
}

func NewIdentityResolutionCache(options IdentityCacheOptions) *IdentityResolutionCache {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	positiveTTL := options.PositiveTTL
	if positiveTTL <= 0 {
		positiveTTL = 5 * time.Minute
	}
	negativeTTL := options.NegativeTTL
	if negativeTTL <= 0 {
		negativeTTL = 30 * time.Second
	}

	return &IdentityResolutionCache{
		positiveTTL: positiveTTL,
		negativeTTL: negativeTTL,
		now:         now,
		entries:     make(map[identityCacheKey]identityCacheEntry),
	}
}

func (c *IdentityResolutionCache) Get(issuer string, subject string) (Identity, error, bool) {
	if c == nil {
		return Identity{}, nil, false
	}

	key := identityCacheKey{issuer: issuer, subject: subject}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return Identity{}, nil, false
	}
	if !c.now().Before(entry.expiresAt) {
		delete(c.entries, key)
		return Identity{}, nil, false
	}
	return entry.identity, entry.err, true
}

func (c *IdentityResolutionCache) SetSuccess(issuer string, subject string, identity Identity) {
	if c == nil {
		return
	}
	c.set(issuer, subject, identity, nil, c.positiveTTL)
}

func (c *IdentityResolutionCache) SetFailure(issuer string, subject string, err error) {
	if c == nil {
		return
	}
	c.set(issuer, subject, Identity{}, err, c.negativeTTL)
}

func (c *IdentityResolutionCache) set(issuer string, subject string, identity Identity, err error, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[identityCacheKey{issuer: issuer, subject: subject}] = identityCacheEntry{
		identity:  identity,
		err:       err,
		expiresAt: c.now().Add(ttl),
	}
}

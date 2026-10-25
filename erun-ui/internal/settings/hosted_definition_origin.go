package settings

import (
	"strings"
	"sync"
)

// definitionWriteOrigin marks config writes this desktop makes during a
// definition transfer, so the config watcher does not re-upload its own write
// in an endless loop. Only the watcher's reaction consumes marks. It is a mark,
// not a content comparison, so an outside write converging on the same bytes is
// not swallowed; an outside change coalesced into the same debounce window is,
// and is caught by the next digest check or launch catch-up.
type definitionWriteOrigin struct {
	mu   sync.Mutex
	self map[string]struct{}
}

func definitionWriteOriginKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "\x00" + strings.TrimSpace(environment)
}

// The caller clears the mark if the transfer ends without writing.
func (o *definitionWriteOrigin) mark(tenant, environment string) {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.self == nil {
		o.self = make(map[string]struct{})
	}
	o.self[definitionWriteOriginKey(tenant, environment)] = struct{}{}
}

// consume clears either way, so a burst spends the mark once; later events are
// stopped by the digest comparison.
func (o *definitionWriteOrigin) consume(tenant, environment string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	key := definitionWriteOriginKey(tenant, environment)
	if _, ok := o.self[key]; !ok {
		return false
	}
	delete(o.self, key)
	return true
}

func (o *definitionWriteOrigin) clear(tenant, environment string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.self, definitionWriteOriginKey(tenant, environment))
}

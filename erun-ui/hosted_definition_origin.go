package main

import (
	"strings"
	"sync"
)

// definitionWriteOrigin is the config watcher's origin filter: it records the
// environment-config writes this desktop itself made while transferring a
// definition, so the watcher can tell its own write from an outside one and
// stops firing on it.
//
// It is consulted by the watcher's reaction (reactToConfigWatchTargets) and
// nowhere else. A mark names a write the watcher is about to be told about, so
// it can only mean anything to the one caller that receives that telling; a
// reusable "does this environment need an upload?" decision — the launch
// catch-up, the panel's own button — asks a different question and must not be
// silenced by a mark it did not set.
//
// A mark is spent by the first reaction for its environment. When no reaction
// comes, nothing consumes it and nothing is swallowed: a mark can only ever
// suppress a reaction, so a desktop with no watcher running simply carries it
// until the next transfer for that environment overwrites it.
//
// It is at-most-once per environment with an explicit clear — the same shape
// emitEnvironmentInitialized and clearInitEmitted already use for the
// create→deploy loop, and for the same reason. Stamping a transfer rewrites
// the environment's config.yaml, which is a file the watcher is armed on: an
// unfiltered watcher would see that write, upload over it, stamp again, and
// see that, one upload per round trip forever. The mark is what breaks the
// cycle at its first step.
//
// What it deliberately is not: a comparison of the file's contents. Inferring
// "this write was mine" from the bytes on disk is a guess, and it is wrong
// exactly when it matters — a writer converging on the settings this desktop
// was about to write is indistinguishable from this desktop writing them, so
// a genuine outside change would be swallowed silently. The mark is set by the
// writer that knows, and by nothing else.
//
// The bound is honest and it is not silent: fsnotify reports that a path
// changed, not that two writers changed it, so an outside change that
// coalesces into the same debounce window as this desktop's own write arrives
// as one event and is consumed by the mark. That change is still visible — the
// digest the transfer stamps is what HostedDefinitionLocalChangeFor compares
// against on the next read — and it is uploaded on the next change or by the
// next launch's catch-up pass.
type definitionWriteOrigin struct {
	mu   sync.Mutex
	self map[string]struct{}
}

func definitionWriteOriginKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "\x00" + strings.TrimSpace(environment)
}

// mark records that this desktop is about to write tenant/environment's config
// as part of a definition transfer. The caller clears it (clear) if the
// transfer ends without writing, so a failed upload cannot leave a live mark
// swallowing the next outside change.
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

// consume reports whether the pending decision for tenant/environment is this
// desktop's own write, clearing the record either way. Consuming clears, so a
// burst that straddles a debounce window spends the mark once; the events that
// fall through after it are stopped by the digest comparison instead, which is
// what keeps the cycle closed without swallowing a second change out of the
// same mark.
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

// clear drops a mark whose write never happened.
func (o *definitionWriteOrigin) clear(tenant, environment string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.self, definitionWriteOriginKey(tenant, environment))
}

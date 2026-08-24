package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewFsnotifyConfigWatcherFailsWhenTheRootCannotBeCreated locks in the
// seam startConfigWatcher now uses to surface its own startup failure
// instead of a bare return.
func TestNewFsnotifyConfigWatcherFailsWhenTheRootCannotBeCreated(t *testing.T) {
	tmp := t.TempDir()
	// A regular file sits where the watcher root needs to be a directory, so
	// os.MkdirAll fails — the same failure shape startConfigWatcher hits when
	// the real config directory is blocked (e.g. a stray file left by another
	// tool, a permissions problem).
	blocker := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(blocker, "erun")

	if _, err := newFsnotifyConfigWatcher(root); err == nil {
		t.Fatal("expected an error when the config directory cannot be created")
	}
}

// TestStartConfigWatcherSurfacesAStartupFailure is the regression for
// erun#1216 bug 4: a failed watcher start must reach the user as an
// actionable notification instead of leaving config-change detection
// silently and permanently off.
func TestStartConfigWatcherSurfacesAStartupFailure(t *testing.T) {
	emits := newCapturedEmits()
	app := &App{emitFn: emits.fn()}

	app.reportConfigWatcherFailure(errors.New("create config directory: mkdir: not a directory"))

	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("want one notification, got %d: %+v", len(events), events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok || payload.Kind != "warning" || payload.Message == "" {
		t.Fatalf("want an actionable warning notification, got %+v", events[0])
	}
}

// TestConfigWatcherSurfacesARuntimeError covers the second swallowed path:
// an error the fsnotify watcher reports after it already started (e.g. an
// overflowed event queue) must not be discarded either.
func TestConfigWatcherSurfacesARuntimeError(t *testing.T) {
	root := t.TempDir()
	watcher, err := newFsnotifyConfigWatcher(root)
	if err != nil {
		t.Fatalf("newFsnotifyConfigWatcher: %v", err)
	}

	notified := make(chan appNotificationPayload, 1)
	app := &App{emitFn: func(name string, args ...any) {
		if name != appNotificationEvent || len(args) == 0 {
			return
		}
		if payload, ok := args[0].(appNotificationPayload); ok {
			notified <- payload
		}
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cw := &configWatcher{watcher: watcher, cancel: cancel, done: make(chan struct{})}
	go app.runConfigWatcher(ctx, cw, root)

	watcher.Errors <- errors.New("boom")

	select {
	case payload := <-notified:
		if payload.Kind != "warning" {
			t.Fatalf("want a warning notification, got %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a notification for the watcher's runtime error")
	}

	cancel()
	_ = watcher.Close()
	<-cw.done
}

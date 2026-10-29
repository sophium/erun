package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestNewFsnotifyConfigWatcherFailsWhenTheRootCannotBeCreated(t *testing.T) {
	tmp := t.TempDir()
	// A file where the root directory belongs makes os.MkdirAll fail.
	blocker := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(blocker, "erun")

	if _, err := newFsnotifyConfigWatcher(root); err == nil {
		t.Fatal("expected an error when the config directory cannot be created")
	}
}

func TestStartConfigWatcherSurfacesAStartupFailure(t *testing.T) {
	emits := newCapturedEmits()
	svc := newTestService(Deps{Emit: emits.fn()})

	svc.reportConfigWatcherFailure(errors.New("create config directory: mkdir: not a directory"))

	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("want one notification, got %d: %+v", len(events), events)
	}
	payload, ok := events[0].(uimodel.AppNotificationPayload)
	if !ok || payload.Kind != "warning" || payload.Message == "" {
		t.Fatalf("want an actionable warning notification, got %+v", events[0])
	}
}

func TestConfigWatcherSurfacesARuntimeError(t *testing.T) {
	root := t.TempDir()
	// Own the channels: a real backend can close Errors while the test sends on it.
	watcher := &fsnotify.Watcher{
		Events: make(chan fsnotify.Event),
		Errors: make(chan error),
	}

	notified := make(chan uimodel.AppNotificationPayload, 1)
	svc := newTestService(Deps{Emit: func(name string, args ...any) {
		if name != uimodel.AppNotificationEvent || len(args) == 0 {
			return
		}
		if payload, ok := args[0].(uimodel.AppNotificationPayload); ok {
			notified <- payload
		}
	}})

	ctx, cancel := context.WithCancel(context.Background())
	cw := &configWatcher{watcher: watcher, cancel: cancel, done: make(chan struct{})}
	go svc.runConfigWatcher(ctx, cw, root)
	t.Cleanup(func() {
		cancel()
		<-cw.done
	})

	watcher.Errors <- errors.New("boom")

	select {
	case payload := <-notified:
		if payload.Kind != "warning" {
			t.Fatalf("want a warning notification, got %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a notification for the watcher's runtime error")
	}
}

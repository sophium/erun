package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	eruncommon "github.com/sophium/erun/erun-common"
)

// configWatcherDebounce coalesces the burst of files erun init writes in
// quick succession (tool, tenant, env config) into one state reload.
const configWatcherDebounce = 250 * time.Millisecond

// configWatcher observes the on-disk erun config tree and notifies the
// frontend when it changes. The dialog/init flow already gets a
// targeted environment-initialized signal from the PTY trace handler;
// this watcher exists to catch the cases that bypass the desktop's PTY
// — `erun init` run from a separate terminal, `erun env delete`, a
// user editing config files by hand, etc. See erun-ui/AGENTS.md
// § "Command Completion And State-Refresh Wiring".
type configWatcher struct {
	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
	done    chan struct{}
}

func (a *App) startConfigWatcher() {
	a.mu.Lock()
	if a.configWatcher != nil {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	root, err := eruncommon.ERunConfigDir()
	if err != nil || root == "" {
		return
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	if err := addConfigWatchDirs(watcher, root); err != nil {
		_ = watcher.Close()
		return
	}

	ctx, cancel := context.WithCancel(a.activityWatcherCtx())
	cw := &configWatcher{
		watcher: watcher,
		cancel:  cancel,
		done:    make(chan struct{}),
	}

	a.mu.Lock()
	a.configWatcher = cw
	a.mu.Unlock()

	go a.runConfigWatcher(ctx, cw, root)
}

func (a *App) stopConfigWatcher() {
	a.mu.Lock()
	cw := a.configWatcher
	a.configWatcher = nil
	a.mu.Unlock()
	if cw == nil {
		return
	}
	if cw.cancel != nil {
		cw.cancel()
	}
	_ = cw.watcher.Close()
	<-cw.done
}

func (a *App) runConfigWatcher(ctx context.Context, cw *configWatcher, root string) {
	defer close(cw.done)

	var emitTimer *time.Timer
	var emitMu sync.Mutex

	queueEmit := func() {
		emitMu.Lock()
		defer emitMu.Unlock()
		if emitTimer != nil {
			emitTimer.Reset(configWatcherDebounce)
			return
		}
		emitTimer = time.AfterFunc(configWatcherDebounce, func() {
			emitMu.Lock()
			emitTimer = nil
			emitMu.Unlock()
			a.emitEnvironmentsChanged()
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			handleConfigWatchEvent(cw.watcher, event, queueEmit)
		case _, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// handleConfigWatchEvent adds newly-created config subdirs to the watch set
// because fsnotify is not recursive and would otherwise miss writes inside them.
func handleConfigWatchEvent(watcher *fsnotify.Watcher, event fsnotify.Event, queueEmit func()) {
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = watcher.Add(event.Name)
		}
	}
	if event.Has(fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename) {
		queueEmit()
	}
}

func addConfigWatchDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		return watcher.Add(path)
	})
}

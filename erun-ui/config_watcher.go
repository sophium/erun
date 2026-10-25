package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sophium/erun/erun-common/base/config"
)

// configWatcherDebounce coalesces erun init's burst of writes into one reload.
const configWatcherDebounce = 250 * time.Millisecond

// configWatcher catches config changes made outside the desktop's PTY (other
// terminals, hand edits). See erun-ui/AGENTS.md § "Command Completion And State-Refresh Wiring".
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

	root, err := config.ERunConfigDir()
	if err != nil {
		a.reportConfigWatcherFailure(fmt.Errorf("resolve config directory: %w", err))
		return
	}
	if root == "" {
		a.reportConfigWatcherFailure(fmt.Errorf("resolve config directory: no path returned"))
		return
	}
	watcher, err := newFsnotifyConfigWatcher(root)
	if err != nil {
		a.reportConfigWatcherFailure(err)
		return
	}

	ctx, cancel := context.WithCancel(a.context())
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

func newFsnotifyConfigWatcher(root string) (*fsnotify.Watcher, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create config directory %s: %w", root, err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("start filesystem watcher: %w", err)
	}
	if err := addConfigWatchDirs(watcher, root); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("watch config directory %s: %w", root, err)
	}
	return watcher, nil
}

// reportConfigWatcherFailure surfaces watcher failures; silence would read as "nothing changed".
func (a *App) reportConfigWatcherFailure(err error) {
	a.emitAppNotification("warning", fmt.Sprintf(
		"Could not watch the config directory for external changes: %s. Environments created or edited outside this window may not appear until you reopen the app.",
		err.Error(),
	))
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
	// runConfigWatcher closes done only after its debounce has stopped and any flush finished.
	<-cw.done
}

// configDebouncer coalesces config-tree events into one flush. Once stop returns,
// no flush is running and none can start, since flushes read process-global config.
type configDebouncer struct {
	root  string
	flush func([]definitionWatchTarget)

	// mu: the callback claims its slot under the same lock stop sets stopped under;
	// timer.Stop alone cannot tell not-yet-scheduled from already-finished.
	mu      sync.Mutex
	stopped bool
	timer   *time.Timer
	running sync.WaitGroup
	pending map[definitionWatchTarget]struct{}

	// flushMu serializes flushes so one change cannot produce two uploaded revisions.
	flushMu sync.Mutex
}

func newConfigDebouncer(root string, flush func([]definitionWatchTarget)) *configDebouncer {
	return &configDebouncer{
		root:    root,
		flush:   flush,
		pending: map[definitionWatchTarget]struct{}{},
	}
}

// observe re-arms the window even for unattributed paths; an empty flush still refreshes state.
func (d *configDebouncer) observe(path string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if target, ok := definitionWatchTargetFor(d.root, path); ok {
		d.pending[target] = struct{}{}
	}
	if d.timer != nil {
		d.timer.Reset(configWatcherDebounce)
		return
	}
	d.timer = time.AfterFunc(configWatcherDebounce, d.run)
}

func (d *configDebouncer) run() {
	d.mu.Lock()
	d.timer = nil
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.running.Add(1)
	d.mu.Unlock()
	defer d.running.Done()

	d.flushMu.Lock()
	defer d.flushMu.Unlock()
	d.flush(d.take())
}

func (d *configDebouncer) take() []definitionWatchTarget {
	d.mu.Lock()
	defer d.mu.Unlock()
	targets := make([]definitionWatchTarget, 0, len(d.pending))
	for target := range d.pending {
		targets = append(targets, target)
	}
	d.pending = map[definitionWatchTarget]struct{}{}
	return targets
}

// stop stops the timer, refuses later callbacks, and waits out an in-flight flush.
func (d *configDebouncer) stop() {
	d.mu.Lock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.mu.Unlock()
	d.running.Wait()
}

func (a *App) runConfigWatcher(ctx context.Context, cw *configWatcher, root string) {
	defer close(cw.done)

	debounce := newConfigDebouncer(root, a.reactToConfigWatchTargets)
	defer debounce.stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			handleConfigWatchEvent(cw.watcher, event, debounce.observe)
		case watchErr, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			a.reportConfigWatcherFailure(fmt.Errorf("watch config directory: %w", watchErr))
		}
	}
}

type definitionWatchTarget struct {
	tenant      string
	environment string
}

// definitionWatchTargetFor attributes an event to an environment's own config.yaml,
// resolved through the store's resolver and matched on the whole path: the root
// config holds AdminToken in plaintext, and dated .bak backups sit beside each config.
func definitionWatchTargetFor(root, path string) (definitionWatchTarget, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return definitionWatchTarget{}, false
	}
	segments := strings.Split(relative, string(os.PathSeparator))
	if len(segments) != 3 {
		return definitionWatchTarget{}, false
	}
	tenant, environment := segments[0], segments[1]
	expected, err := config.EnvConfigPath(tenant, environment)
	if err != nil || filepath.Clean(expected) != filepath.Clean(path) {
		return definitionWatchTarget{}, false
	}
	return definitionWatchTarget{tenant: tenant, environment: environment}, true
}

// fsnotify is not recursive, so newly created subdirs must be added.
func handleConfigWatchEvent(watcher *fsnotify.Watcher, event fsnotify.Event, queueEmit func(string)) {
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = watcher.Add(event.Name)
		}
	}
	if event.Has(fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename) {
		queueEmit(event.Name)
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

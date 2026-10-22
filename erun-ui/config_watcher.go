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

// newFsnotifyConfigWatcher creates the fsnotify watcher rooted at root and
// arms it on every existing subdirectory, naming which step failed so the
// caller can surface it instead of leaving the watcher silently unstarted.
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

// reportConfigWatcherFailure surfaces a config watcher problem as an
// actionable notification. Swallowing it — the previous behavior, for both a
// failed start and a runtime error from the watcher itself — left config
// changes made outside the desktop's own PTY (a hand-edited file, `erun env
// delete` from another terminal, `erun init` in a separate shell) silently
// unreflected, indistinguishable from "nothing changed".
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
	// runConfigWatcher does not close done until its debounce timer is stopped
	// and any flush already running has finished, so once this returns no
	// goroutine the watcher started is still reading config.
	<-cw.done
}

// configDebouncer coalesces a burst of config-tree events into one flush of
// the environments they touched, and owns the work that flush starts.
//
// Owning that work is the whole reason this is a type rather than a handful of
// locals. A flush reads an environment's config, so a debounce callback still
// running after the watcher was stopped reads process-global config state
// whose lifetime its caller owns — `stop` is the contract that prevents it, and
// it has to outlive any single closure to be one: once `stop` returns, no
// flush is running and no further one can start.
type configDebouncer struct {
	root  string
	flush func([]definitionWatchTarget)

	// mu guards the debounce state. The callback claims its slot under this
	// lock, the same one `stop` sets stopped under, so a callback either
	// claims before teardown — and is waited for — or sees stopped set and
	// returns without reading. Stopping the timer alone cannot say which of
	// those happened: Stop reports false both for a callback that has not been
	// scheduled yet and for one that has already finished.
	mu      sync.Mutex
	stopped bool
	timer   *time.Timer
	running sync.WaitGroup
	pending map[definitionWatchTarget]struct{}

	// flushMu keeps two debounce windows from overlapping. A flush performs a
	// platform round trip, and two concurrent flushes for one environment
	// would each decide their own upload from a pre-write read of the same
	// config — two revisions written for one change.
	flushMu sync.Mutex
}

func newConfigDebouncer(root string, flush func([]definitionWatchTarget)) *configDebouncer {
	return &configDebouncer{
		root:    root,
		flush:   flush,
		pending: map[definitionWatchTarget]struct{}{},
	}
}

// observe records one config-tree event. It (re)arms the debounce window
// whether or not the path attributes to an environment, because a flush
// carrying no targets still refreshes the state every change implies.
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

// run is the debounce callback: it claims its slot, drains what was queued, and
// flushes it.
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

// take drains the queued targets, so one change is reported once.
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

// stop ends the debounce: it stops the pending timer, refuses any later
// callback, and waits out a flush already in flight.
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

	// Stopped before done closes, so the caller waiting on done knows no flush
	// is still reading.
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

// definitionWatchTarget is the environment a config-tree event was attributed
// to — the unit the watcher decides an upload for.
type definitionWatchTarget struct {
	tenant      string
	environment string
}

// definitionWatchTargetFor attributes a config-tree event to an environment,
// and reports false for everything else under the root.
//
// Only an environment's own config.yaml counts, and the path is resolved back
// through the config store's own resolver rather than pattern-matched, so the
// names that pass are exactly the ones the store would accept. The rest of the
// tree is deliberately out of reach rather than merely skipped: the root
// config.yaml holds CloudContextConfig.AdminToken in plaintext, and every live
// config sits beside its own dated backups (`config.yaml.2026-09-25.bak`),
// which is why the match is on the whole resolved path and not on a suffix.
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
	expected, err := eruncommon.EnvConfigPath(tenant, environment)
	if err != nil || filepath.Clean(expected) != filepath.Clean(path) {
		return definitionWatchTarget{}, false
	}
	return definitionWatchTarget{tenant: tenant, environment: environment}, true
}

// handleConfigWatchEvent adds newly-created config subdirs to the watch set
// because fsnotify is not recursive and would otherwise miss writes inside them.
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

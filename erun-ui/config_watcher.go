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
	<-cw.done
}

func (a *App) runConfigWatcher(ctx context.Context, cw *configWatcher, root string) {
	defer close(cw.done)

	var emitTimer *time.Timer
	var emitMu sync.Mutex
	// flushMu keeps two debounce windows from overlapping. A flush performs a
	// platform round trip, and two concurrent flushes for one environment
	// would each decide their own upload from a pre-write read of the same
	// config — two revisions written for one change.
	var flushMu sync.Mutex
	pending := map[definitionWatchTarget]struct{}{}

	flush := func() {
		flushMu.Lock()
		defer flushMu.Unlock()
		emitMu.Lock()
		targets := make([]definitionWatchTarget, 0, len(pending))
		for target := range pending {
			targets = append(targets, target)
		}
		pending = map[definitionWatchTarget]struct{}{}
		emitMu.Unlock()

		a.reactToConfigWatchTargets(targets)
	}

	queueEmit := func(path string) {
		emitMu.Lock()
		defer emitMu.Unlock()
		if target, ok := definitionWatchTargetFor(root, path); ok {
			pending[target] = struct{}{}
		}
		if emitTimer != nil {
			emitTimer.Reset(configWatcherDebounce)
			return
		}
		emitTimer = time.AfterFunc(configWatcherDebounce, func() {
			emitMu.Lock()
			emitTimer = nil
			emitMu.Unlock()
			flush()
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

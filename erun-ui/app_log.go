package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The desktop is normally launched via `open` (macOS) or the equivalent
// detached launch on other platforms, which gives it no controlling terminal
// -- its stderr goes nowhere. Every log.Printf in this module writes only to
// the default logger's stderr output, so a partial-wiring skip logged that
// way leaves no trace any later investigation can find. This gives
// the default logger a second, durable destination: log.SetOutput is the one
// place that has to change for every existing and future log.Printf call
// site to survive.

// appLogFileName is the durable log every log.Printf call in this module ends
// up writing to, beside the other per-install state under UserConfigDir()/ERun.
const appLogFileName = "erun-app.log"

// appLogMaxBytes bounds the log so a process that runs for weeks cannot grow
// it without limit. Matches the cap erun-common's per-env trace log uses for
// the same reason.
const appLogMaxBytes = 5 * 1024 * 1024

// appLogPath resolves the durable log file's path.
func appLogPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "ERun", appLogFileName), nil
}

// boundedLogFile is an io.Writer over a size-capped file: once a write would
// push it past maxBytes, the current file is rotated to a single ".1" backup
// and a fresh one started, so the log never grows without bound across a
// desktop process that can run for weeks.
type boundedLogFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	written  int64
}

// newBoundedLogFile opens path for append, creating its directory if needed,
// and picks up the current size so rotation accounts for bytes a previous
// process already wrote.
func newBoundedLogFile(path string, maxBytes int64) (*boundedLogFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	written := int64(0)
	if info, statErr := file.Stat(); statErr == nil {
		written = info.Size()
	}
	return &boundedLogFile{path: path, maxBytes: maxBytes, file: file, written: written}, nil
}

func (b *boundedLogFile) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.written+int64(len(p)) > b.maxBytes {
		if err := b.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := b.file.Write(p)
	b.written += int64(n)
	return n, err
}

// rotate replaces the current file with a fresh one, keeping exactly one
// backup -- the same one-backup-pair shape erun-common's env trace log uses.
func (b *boundedLogFile) rotate() error {
	if err := b.file.Close(); err != nil {
		return err
	}
	backup := b.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(b.path, backup); err != nil {
		return err
	}
	file, err := os.OpenFile(b.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b.file = file
	b.written = 0
	return nil
}

func (b *boundedLogFile) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.file.Close()
}

// initDurableAppLog redirects the default logger so every log.Printf call in
// this module writes to stderr AND the bounded durable log file. Best-effort:
// if the file cannot be opened, the app still runs with stderr-only logging
// rather than failing to start over a diagnostics nicety. The returned func
// closes the file; call it on shutdown.
func initDurableAppLog() func() {
	path, err := appLogPath()
	if err != nil {
		log.Printf("erun-app: could not resolve the durable log path: %v", err)
		return func() {}
	}
	file, err := newBoundedLogFile(path, appLogMaxBytes)
	if err != nil {
		log.Printf("erun-app: could not open the durable log %s: %v", path, err)
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, file))
	log.Printf("erun-app: logging to %s", path)
	return func() { _ = file.Close() }
}

// appLogTailBytes bounds how much of the durable app log one Diagnostics
// console read transfers, matching the env trace log's own cap.
const appLogTailBytes = 64 * 1024

// uiAppLog is the Diagnostics console's read model for the desktop's own
// durable log: the natural evidence for an orchestrator or app-level fault,
// neither of which has an env trace to fall back on.
type uiAppLog struct {
	Available bool   `json:"available"`
	Content   string `json:"content,omitempty"`
	Path      string `json:"path"`
	Reason    string `json:"reason,omitempty"`
}

// LoadAppLog reads the tail of the desktop's own log for the Diagnostics
// console's orchestrator and app contexts. Read-only, like LoadEnvTrace.
func (a *App) LoadAppLog() (uiAppLog, error) {
	path, err := appLogPath()
	if err != nil {
		return uiAppLog{}, err
	}
	result := uiAppLog{Path: path}
	content, err := tailFile(path, appLogTailBytes)
	if err != nil {
		if os.IsNotExist(err) {
			result.Reason = "no log captured yet"
			return result, nil
		}
		return uiAppLog{}, err
	}
	if strings.TrimSpace(content) == "" {
		result.Reason = "no log captured yet"
		return result, nil
	}
	result.Available = true
	result.Content = content
	return result, nil
}

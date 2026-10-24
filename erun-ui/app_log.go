package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The desktop is launched detached, so stderr goes nowhere; log.SetOutput also
// writes every log.Printf to a durable file.

const appLogFileName = "erun-app.log"

const appLogMaxBytes = 5 * 1024 * 1024

func appLogPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "ERun", appLogFileName), nil
}

// boundedLogFile rotates to a single ".1" backup once a write would exceed maxBytes.
type boundedLogFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	written  int64
}

// newBoundedLogFile opens path for append, counting bytes a previous process wrote.
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

// rotate replaces the current file, keeping exactly one backup.
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

// initDurableAppLog tees the default logger to the durable file, falling back to
// stderr-only if it cannot be opened. The returned func closes the file.
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

const appLogTailBytes = 64 * 1024

// uiAppLog is the Diagnostics console's view of the desktop's own log.
type uiAppLog struct {
	Available bool   `json:"available"`
	Content   string `json:"content,omitempty"`
	Path      string `json:"path"`
	Reason    string `json:"reason,omitempty"`
}

// LoadAppLog reads the tail of the desktop's own log.
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

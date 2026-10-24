package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFilePerm is the mode for every file under the user config root: the root,
// tenant and per-environment config.yaml and their dated backups. Those files hold
// cluster admin tokens, and a file mode travels with the file into backups, tarballs
// and support bundles, so it must not rely on the containing directory being 0700.
const ConfigFilePerm os.FileMode = 0o600

// WriteFileAtomic writes via a sibling temp file, fsync, then rename so a crash
// or kill mid-write leaves either the previous contents or no change at all —
// never a 0-byte or partially written file. The rename is atomic because the temp
// file shares the destination's directory, and thus its filesystem.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileViaRename(path, data, perm, true)
}

// ReplaceFileAtomically gives a concurrent reader WriteFileAtomic's guarantee --
// the whole previous file or the whole new one, never a truncated one -- without
// its fsync. Activity snapshots and lease records are rewritten on hot paths
// (the MCP server records one per request) and are read concurrently by idle
// polls; what they need is visibility atomicity, not crash durability.
func ReplaceFileAtomically(path string, data []byte, perm os.FileMode) error {
	return writeFileViaRename(path, data, perm, false)
}

func writeFileViaRename(path string, data []byte, perm os.FileMode, durable bool) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if durable {
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// configReadRetryAttempts/configReadRetrySleep bound how long a read waits out
// a torn write before surfacing ErrConfigCorrupted. WriteFileAtomic makes erun's
// own writers atomic, but a reader still has to tolerate a write it does not
// control -- an external editor saving in place, another process's own
// non-atomic writer, a crash mid-write -- and a torn read from any of those is
// transient by construction: it resolves itself within microseconds, well
// under this budget. A file that still fails to parse after every retry is
// genuinely corrupt, not merely caught mid-write.
const (
	configReadRetryAttempts = 5
	configReadRetrySleep    = 20 * time.Millisecond
)

// loadConfigFile reads path and unmarshals it into out, retrying through
// configReadRetryAttempts when the file is momentarily empty or fails to
// parse. Every Load*Config function in this file routes its read through
// here so the retry policy lives in one place. A missing file is a real
// absence (ErrNotInitialized) and is never retried; an empty or unparseable
// file is retried and, if it never recovers, reported as ErrConfigCorrupted.
// A read that fails for any other reason (permission denied, the path is a
// directory, too many open files, ...) is neither of those things and is
// returned as-is: reporting it as ErrNotInitialized would tell the operator
// to run `erun init` for a problem `erun init` cannot fix.
// configReadRetryObserved, when non-nil, runs synchronously after each failed
// attempt instead of the real sleep. A test sets it to heal a torn read
// deterministically (write the real content the moment a retry is about to
// happen) rather than racing a concurrent writer goroutine against this
// function's wall-clock sleep budget, which is exactly the kind of timing
// race that flakes under CPU contention from unrelated tests running
// alongside it in the same `go test ./...` process.
var ConfigReadRetryObserved func(attempt int)

func LoadConfigFile(path string, out any) error {
	var lastErr error
	for attempt := 0; attempt < configReadRetryAttempts; attempt++ {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return ErrNotInitialized
			}
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			lastErr = ErrConfigCorrupted
		} else if err := yaml.Unmarshal(data, out); err != nil {
			lastErr = fmt.Errorf("%w: %w", ErrConfigCorrupted, err)
		} else {
			return nil
		}
		if attempt < configReadRetryAttempts-1 {
			if ConfigReadRetryObserved != nil {
				ConfigReadRetryObserved(attempt)
			} else {
				time.Sleep(configReadRetrySleep)
			}
		}
	}
	return lastErr
}

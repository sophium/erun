package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFilePerm is 0600 because config files hold cluster admin tokens and the mode travels into copies.
const ConfigFilePerm os.FileMode = 0o600

// WriteFileAtomic writes via a same-directory temp file, fsync, and rename, so a crash never leaves a partial file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileViaRename(path, data, perm, true)
}

// ReplaceFileAtomically is WriteFileAtomic without fsync, for hot paths that need visibility atomicity only.
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

// A reader retries briefly to ride out torn writes from non-atomic external writers;
// a file that still fails to parse is corrupt.
const (
	configReadRetryAttempts = 5
	configReadRetrySleep    = 20 * time.Millisecond
)

// ConfigReadRetryObserved, when set, replaces the retry sleep so tests can heal a torn read deterministically.
var ConfigReadRetryObserved func(attempt int)

// LoadConfigFile unmarshals path into out, retrying torn reads. A missing file is
// ErrNotInitialized; any other read error is returned as-is, since `erun init` cannot fix it.
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

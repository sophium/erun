package config

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

const configBackupSuffix = ".bak"

// configBackupKeep caps retained backups per config file, by count only.
const configBackupKeep = 5

var TimeNow = time.Now

// writeConfigBackupIfDue snapshots the live config at most once per UTC day, best-effort.
func writeConfigBackupIfDue(livePath string, keep int, now func() time.Time) error {
	if strings.TrimSpace(livePath) == "" {
		return errors.New("live path is required")
	}
	if now == nil {
		now = time.Now
	}
	current, err := os.ReadFile(livePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	dir := filepath.Dir(livePath)
	base := filepath.Base(livePath)
	backupPath := filepath.Join(dir, configBackupName(base, now()))
	if _, err := os.Stat(backupPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := WriteFileAtomic(backupPath, current, ConfigFilePerm); err != nil {
		return err
	}
	return pruneOldConfigBackups(dir, base, keep)
}

// pruneOldConfigBackups evicts only backups with its own YYYY-MM-DD names.
func pruneOldConfigBackups(dir, base string, keep int) error {
	if keep <= 0 {
		return nil
	}
	backups, err := ListManagedConfigBackups(dir, base)
	if err != nil {
		return err
	}
	if len(backups) <= keep {
		return nil
	}
	for _, victim := range backups[keep:] {
		if err := os.Remove(victim.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func ListManagedConfigBackups(dir, base string) ([]model.ConfigBackup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	backups := make([]model.ConfigBackup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, ok := parseConfigBackupName(base, entry.Name())
		if !ok {
			continue
		}
		backups = append(backups, model.ConfigBackup{
			Path: filepath.Join(dir, entry.Name()),
			Date: date,
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Date.After(backups[j].Date)
	})
	return backups, nil
}

func configBackupName(base string, t time.Time) string {
	return base + "." + t.UTC().Format("2006-01-02") + configBackupSuffix
}

func parseConfigBackupName(base, name string) (time.Time, bool) {
	prefix := base + "."
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	if !strings.HasSuffix(name, configBackupSuffix) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, prefix), configBackupSuffix)
	parsed, err := time.Parse("2006-01-02", stamp)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func WriteRootConfigBackupIfDue(livePath string, now func() time.Time) error {
	return writeConfigBackupIfDue(livePath, configBackupKeep, now)
}

func WriteEnvConfigBackupIfDue(livePath string, now func() time.Time) error {
	return writeConfigBackupIfDue(livePath, configBackupKeep, now)
}

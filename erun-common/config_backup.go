package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

const configBackupSuffix = ".bak"

// configBackupKeep caps retained backups per config file; eviction is purely by count, never by age.
const configBackupKeep = 5

var timeNow = time.Now

// ConfigBackup describes one dated backup file on disk. Date is parsed from
// the filename, not the filesystem mtime, so rotation order stays correct
// even where file timestamps are imprecise.
type ConfigBackup struct {
	Path string
	Date time.Time
}

// writeConfigBackupIfDue is best-effort and snapshots the live config at most
// once per UTC day: the sole "back up now" signal is an empty slot for today's
// date, with no dependency on timers, mtimes, or external state.
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
	if err := WriteFileAtomic(backupPath, current, 0o644); err != nil {
		return err
	}
	return pruneOldConfigBackups(dir, base, keep)
}

func listConfigBackups(livePath string) ([]ConfigBackup, error) {
	if strings.TrimSpace(livePath) == "" {
		return nil, errors.New("live path is required")
	}
	dir := filepath.Dir(livePath)
	base := filepath.Base(livePath)
	return listManagedConfigBackups(dir, base)
}

func findConfigBackupByDate(livePath, date string) (ConfigBackup, bool, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return ConfigBackup{}, false, errors.New("date is required")
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ConfigBackup{}, false, fmt.Errorf("invalid backup date %q: %w", date, err)
	}
	backups, err := listConfigBackups(livePath)
	if err != nil {
		return ConfigBackup{}, false, err
	}
	for _, backup := range backups {
		if backup.Date.Equal(parsed) {
			return backup, true, nil
		}
	}
	return ConfigBackup{}, false, nil
}

// restoreConfigFromBackup validates that a backup deserializes cleanly before
// overwriting the live config, so a corrupted backup can never replace a good one.
func restoreConfigFromBackup(backupPath, livePath string, validate func(backupPath string, data []byte) error) error {
	if strings.TrimSpace(backupPath) == "" || strings.TrimSpace(livePath) == "" {
		return errors.New("backup and live paths are required")
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("backup %s is empty", backupPath)
	}
	if err := validate(backupPath, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(livePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}
	if err := WriteFileAtomic(livePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

// pruneOldConfigBackups only counts and evicts backups it wrote (parseable
// YYYY-MM-DD names); foreign .bak files in the directory are left untouched.
func pruneOldConfigBackups(dir, base string, keep int) error {
	if keep <= 0 {
		return nil
	}
	backups, err := listManagedConfigBackups(dir, base)
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

func listManagedConfigBackups(dir, base string) ([]ConfigBackup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	backups := make([]ConfigBackup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, ok := parseConfigBackupName(base, entry.Name())
		if !ok {
			continue
		}
		backups = append(backups, ConfigBackup{
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

// ---- Root config backup API ------------------------------------------------

func writeRootConfigBackupIfDue(livePath string, now func() time.Time) error {
	return writeConfigBackupIfDue(livePath, configBackupKeep, now)
}

// ListRootConfigBackups returns the root config's dated backups, newest
// first.
func ListRootConfigBackups(livePath string) ([]ConfigBackup, error) {
	return listConfigBackups(livePath)
}

// FindRootConfigBackupByDate resolves a root-config backup by its
// YYYY-MM-DD stamp.
func FindRootConfigBackupByDate(livePath, date string) (ConfigBackup, bool, error) {
	return findConfigBackupByDate(livePath, date)
}

// RestoreRootConfigFromBackup copies a root-config backup over the live
// root config after confirming the bytes deserialize into an ERunConfig.
func RestoreRootConfigFromBackup(backupPath, livePath string) error {
	return restoreConfigFromBackup(backupPath, livePath, func(path string, data []byte) error {
		probe := ERunConfig{}
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return fmt.Errorf("backup %s is not a valid erun config: %w", path, err)
		}
		return nil
	})
}

// ---- Environment config backup API -----------------------------------------

func writeEnvConfigBackupIfDue(livePath string, now func() time.Time) error {
	return writeConfigBackupIfDue(livePath, configBackupKeep, now)
}

// ListEnvConfigBackups returns the dated backups for one environment's
// config, newest first.
func ListEnvConfigBackups(tenant, environment string) ([]ConfigBackup, error) {
	livePath, err := EnvConfigPath(tenant, environment)
	if err != nil {
		return nil, err
	}
	return listConfigBackups(livePath)
}

// FindEnvConfigBackupByDate resolves an environment-config backup by its
// YYYY-MM-DD stamp.
func FindEnvConfigBackupByDate(tenant, environment, date string) (ConfigBackup, bool, error) {
	livePath, err := EnvConfigPath(tenant, environment)
	if err != nil {
		return ConfigBackup{}, false, err
	}
	return findConfigBackupByDate(livePath, date)
}

// RestoreEnvConfigFromBackup copies an environment-config backup over
// the live env config after confirming the bytes deserialize into an
// EnvConfig (which also runs the legacy-field migration). Used by
// `erun doctor --restore-env-config-from-backup` to recover an env whose
// config was changed or corrupted — for example a type silently
// resolved to the wrong value.
func RestoreEnvConfigFromBackup(backupPath, tenant, environment string) error {
	livePath, err := EnvConfigPath(tenant, environment)
	if err != nil {
		return err
	}
	return restoreConfigFromBackup(backupPath, livePath, func(path string, data []byte) error {
		probe := EnvConfig{}
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return fmt.Errorf("backup %s is not a valid environment config: %w", path, err)
		}
		return nil
	})
}

// EnvConfigPath resolves the on-disk path of one environment's config file.
func EnvConfigPath(tenant, environment string) (string, error) {
	path, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, environment, configFile))
	if err != nil {
		return "", ErrNoUserDataFolder
	}
	return path, nil
}

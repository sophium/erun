package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"gopkg.in/yaml.v3"
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

func listConfigBackups(livePath string) ([]model.ConfigBackup, error) {
	if strings.TrimSpace(livePath) == "" {
		return nil, errors.New("live path is required")
	}
	dir := filepath.Dir(livePath)
	base := filepath.Base(livePath)
	return listManagedConfigBackups(dir, base)
}

func findConfigBackupByDate(livePath, date string) (model.ConfigBackup, bool, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return model.ConfigBackup{}, false, errors.New("date is required")
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return model.ConfigBackup{}, false, fmt.Errorf("invalid backup date %q: %w", date, err)
	}
	backups, err := listConfigBackups(livePath)
	if err != nil {
		return model.ConfigBackup{}, false, err
	}
	for _, backup := range backups {
		if backup.Date.Equal(parsed) {
			return backup, true, nil
		}
	}
	return model.ConfigBackup{}, false, nil
}

// restoreConfigFromBackup validates a backup before it overwrites the live config.
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
	if err := WriteFileAtomic(livePath, data, ConfigFilePerm); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

// pruneOldConfigBackups evicts only backups with its own YYYY-MM-DD names.
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

func listManagedConfigBackups(dir, base string) ([]model.ConfigBackup, error) {
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

// ListRootConfigBackups returns the root config's backups, newest first.
func ListRootConfigBackups(livePath string) ([]model.ConfigBackup, error) {
	return listConfigBackups(livePath)
}

// FindRootConfigBackupByDate resolves a root-config backup by its YYYY-MM-DD stamp.
func FindRootConfigBackupByDate(livePath, date string) (model.ConfigBackup, bool, error) {
	return findConfigBackupByDate(livePath, date)
}

// RestoreRootConfigFromBackup restores a root-config backup after validating it.
func RestoreRootConfigFromBackup(backupPath, livePath string) error {
	return restoreConfigFromBackup(backupPath, livePath, func(path string, data []byte) error {
		probe := ERunConfig{}
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return fmt.Errorf("backup %s is not a valid erun config: %w", path, err)
		}
		return nil
	})
}

func WriteEnvConfigBackupIfDue(livePath string, now func() time.Time) error {
	return writeConfigBackupIfDue(livePath, configBackupKeep, now)
}

// ListEnvConfigBackups returns one environment config's backups, newest first.
func ListEnvConfigBackups(tenant, environment string) ([]model.ConfigBackup, error) {
	livePath, err := EnvConfigPath(tenant, environment)
	if err != nil {
		return nil, err
	}
	return listConfigBackups(livePath)
}

// FindEnvConfigBackupByDate resolves an environment-config backup by its YYYY-MM-DD stamp.
func FindEnvConfigBackupByDate(tenant, environment, date string) (model.ConfigBackup, bool, error) {
	livePath, err := EnvConfigPath(tenant, environment)
	if err != nil {
		return model.ConfigBackup{}, false, err
	}
	return findConfigBackupByDate(livePath, date)
}

// RestoreEnvConfigFromBackup restores an environment-config backup after validating it.
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

// EnvConfigPath returns the path of one environment's config file.
func EnvConfigPath(tenant, environment string) (string, error) {
	if err := ValidateStatePathSegment("tenant", tenant); err != nil {
		return "", err
	}
	if err := ValidateStatePathSegment("environment", environment); err != nil {
		return "", err
	}
	path, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, environment, ConfigFile))
	if err != nil {
		return "", ErrNoUserDataFolder
	}
	return path, nil
}

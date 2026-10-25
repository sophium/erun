package configbackup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"gopkg.in/yaml.v3"
)

func listConfigBackups(livePath string) ([]model.ConfigBackup, error) {
	if strings.TrimSpace(livePath) == "" {
		return nil, errors.New("live path is required")
	}
	dir := filepath.Dir(livePath)
	base := filepath.Base(livePath)
	return config.ListManagedConfigBackups(dir, base)
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
		return config.ErrNoUserDataFolder
	}
	if err := config.WriteFileAtomic(livePath, data, config.ConfigFilePerm); err != nil {
		return config.ErrFailedToSaveConfig
	}
	return nil
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
		probe := config.ERunConfig{}
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return fmt.Errorf("backup %s is not a valid erun config: %w", path, err)
		}
		return nil
	})
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
		probe := config.EnvConfig{}
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return fmt.Errorf("backup %s is not a valid environment config: %w", path, err)
		}
		return nil
	})
}

// EnvConfigPath returns the path of one environment's config file.
func EnvConfigPath(tenant, environment string) (string, error) {
	if err := config.ValidateStatePathSegment("tenant", tenant); err != nil {
		return "", err
	}
	if err := config.ValidateStatePathSegment("environment", environment); err != nil {
		return "", err
	}
	path, err := config.ResolveConfigFilePath(filepath.Join(config.ConfigRoot, tenant, environment, config.ConfigFile))
	if err != nil {
		return "", config.ErrNoUserDataFolder
	}
	return path, nil
}

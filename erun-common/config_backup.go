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

// configBackupSuffix is the trailing extension used for every dated
// backup file. The full backup name follows the shape
// "<live-basename>.<YYYY-MM-DD>.bak", written into the same directory
// as the live config so the atomic rename guarantee inside
// writeFileAtomic still applies to backup creation.
const configBackupSuffix = ".bak"

// configBackupKeep is the cap on retained backups per config file. The
// policy is purely count-based: when a new daily backup pushes the file
// count past this number, the oldest existing backup is evicted. Backups
// are never pruned by age, so a pre-existing 10-day-old file is kept
// until 5 newer dailies push it out. The same cap applies to the root
// config and to each per-environment config (each env directory keeps
// its own independent rotation).
const configBackupKeep = 5

// timeNow is the injectable package clock threaded into the backup
// writers as their date stamp source. Tests swap it with a fixed clock;
// production keeps the default time.Now.
var timeNow = time.Now

// ConfigBackup describes one dated backup file on disk. The Date field
// is parsed from the filename (not the filesystem mtime) so rotation
// order stays correct even on platforms with imprecise file
// timestamps. The same descriptor covers the root config and per-env
// config backups; they differ only in which directory they live in.
type ConfigBackup struct {
	Path string
	Date time.Time
}

// writeConfigBackupIfDue snapshots the *current* contents of livePath
// under "<livePath>.<YYYY-MM-DD>.bak" using the supplied clock for the
// date stamp, keeping at most keep dailies. It is intentionally a
// best-effort operation:
//
//   - When the live file does not exist yet (true first-write), there
//     is nothing to back up; returns nil.
//   - When today's dated backup already exists, the function is a
//     no-op so multiple saves in the same UTC date do not rewrite the
//     same snapshot or evict newer rotation slots.
//   - After a successful write it calls pruneOldConfigBackups to
//     enforce the count-based retention policy.
//
// The "if due" wording is deliberate: the only signal for "back up
// now" is "today's slot is empty." There is no I/O dependency on
// timers, mtimes, or external state.
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
	if err := writeFileAtomic(backupPath, current, 0o644); err != nil {
		return err
	}
	return pruneOldConfigBackups(dir, base, keep)
}

// listConfigBackups enumerates every dated backup found next to the
// supplied live config path, newest first. Filenames that fail to parse
// as a valid YYYY-MM-DD stamp are skipped silently — the rotation
// policy never created them, so they are out-of-band and stay where
// they are.
func listConfigBackups(livePath string) ([]ConfigBackup, error) {
	if strings.TrimSpace(livePath) == "" {
		return nil, errors.New("live path is required")
	}
	dir := filepath.Dir(livePath)
	base := filepath.Base(livePath)
	return listManagedConfigBackups(dir, base)
}

// findConfigBackupByDate returns the backup whose dated suffix matches
// the supplied YYYY-MM-DD string. Used by the restore flows to resolve
// a user-supplied date without doing the full enumeration twice.
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

// restoreConfigFromBackup copies the bytes of the supplied backup file
// over the live config path, after validating that the bytes
// deserialize cleanly via validate. The validation is the whole point
// of routing the restore through this helper instead of a plain cp: a
// corrupted backup must not replace a (possibly less corrupted) live
// file. Restores via the atomic-write helper so a crash mid-restore
// cannot itself produce a partial file.
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
	if err := writeFileAtomic(livePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

// pruneOldConfigBackups enforces the count-based retention policy.
// Backups whose filenames do not parse as YYYY-MM-DD are considered
// out-of-band and are not counted toward the retention budget; they are
// also not removed. This is intentional: the policy is "evict by count
// among files we wrote" rather than "delete any .bak in this directory."
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

// writeRootConfigBackupIfDue snapshots the live root config before an
// overwrite. Thin wrapper over the shared core; preserved as a named
// entrypoint because the root save path reads more clearly with it.
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

// writeEnvConfigBackupIfDue snapshots a live per-environment config
// before an overwrite. Thin wrapper over the shared core; the env save
// path and the in-pod config sync both call it so a changed env config
// (e.g. a flipped type) leaves a recoverable trail.
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

// EnvConfigPath resolves the on-disk path of one environment's
// config.yaml, mirroring the layout SaveEnvConfig/LoadEnvConfig use.
// Exported so the doctor restore flow can trace the destination of a
// per-env restore in --dry-run.
func EnvConfigPath(tenant, environment string) (string, error) {
	path, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, environment, configFile))
	if err != nil {
		return "", ErrNoUserDataFolder
	}
	return path, nil
}

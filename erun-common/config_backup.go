package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// rootConfigBackupSuffix is the trailing extension used for every
// dated backup file. The full backup name follows the shape
// "<live-basename>.<YYYY-MM-DD>.bak", written into the same directory
// as the live root config so the atomic rename guarantee inside
// writeFileAtomic still applies to backup creation.
const rootConfigBackupSuffix = ".bak"

// rootConfigBackupKeep is the cap on retained backups. The policy is
// purely count-based: when a new daily backup pushes the file count
// past this number, the oldest existing backup is evicted. Backups
// are never pruned by age, so a pre-existing 10-day-old file is kept
// until 5 newer dailies push it out.
const rootConfigBackupKeep = 5

// timeNow is the package-level clock used by every time-sensitive
// helper in this file (backup naming, rotation, listing). Tests swap
// it with a fixed clock; production keeps the default time.Now.
// Defined here so the backup code is the only consumer that needs to
// know about clock injection.
var timeNow = time.Now

// RootConfigBackup describes one dated backup file on disk. The Date
// field is parsed from the filename (not the filesystem mtime) so
// rotation order stays correct even on platforms with imprecise file
// timestamps.
type RootConfigBackup struct {
	Path string
	Date time.Time
}

// writeRootConfigBackupIfDue snapshots the *current* contents of the
// live root config file under "<livePath>.<YYYY-MM-DD>.bak" using
// the supplied clock for the date stamp. It is intentionally a
// best-effort operation:
//
//   - When the live file does not exist yet (true first-write), there
//     is nothing to back up; returns nil.
//   - When today's dated backup already exists, the function is a
//     no-op so multiple saves in the same UTC date do not rewrite the
//     same snapshot or evict newer rotation slots.
//   - After a successful write it calls pruneOldRootConfigBackups to
//     enforce the count-based retention policy.
//
// The "if due" wording is deliberate: the only signal for "back up
// now" is "today's slot is empty." There is no I/O dependency on
// timers, mtimes, or external state.
func writeRootConfigBackupIfDue(livePath string, now func() time.Time) error {
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
	backupPath := filepath.Join(dir, rootConfigBackupName(base, now()))
	if _, err := os.Stat(backupPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeFileAtomic(backupPath, current, 0o644); err != nil {
		return err
	}
	return pruneOldRootConfigBackups(dir, base, rootConfigBackupKeep)
}

// ListRootConfigBackups enumerates every dated backup found next to
// the supplied live config path, newest first. Filenames that fail to
// parse as a valid YYYY-MM-DD stamp are skipped silently — the
// rotation policy never created them, so they are out-of-band and
// stay where they are.
func ListRootConfigBackups(livePath string) ([]RootConfigBackup, error) {
	if strings.TrimSpace(livePath) == "" {
		return nil, errors.New("live path is required")
	}
	dir := filepath.Dir(livePath)
	base := filepath.Base(livePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	backups := make([]RootConfigBackup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, ok := parseRootConfigBackupName(base, entry.Name())
		if !ok {
			continue
		}
		backups = append(backups, RootConfigBackup{
			Path: filepath.Join(dir, entry.Name()),
			Date: date,
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Date.After(backups[j].Date)
	})
	return backups, nil
}

// FindRootConfigBackupByDate returns the backup whose dated suffix
// matches the supplied YYYY-MM-DD string. Used by the CLI's
// --restore-config-from-backup flow to resolve a user-supplied date
// without doing the full enumeration twice.
func FindRootConfigBackupByDate(livePath, date string) (RootConfigBackup, bool, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		return RootConfigBackup{}, false, errors.New("date is required")
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return RootConfigBackup{}, false, fmt.Errorf("invalid backup date %q: %w", date, err)
	}
	backups, err := ListRootConfigBackups(livePath)
	if err != nil {
		return RootConfigBackup{}, false, err
	}
	for _, backup := range backups {
		if backup.Date.Equal(parsed) {
			return backup, true, nil
		}
	}
	return RootConfigBackup{}, false, nil
}

// RestoreRootConfigFromBackup copies the bytes of the supplied backup
// file over the live root config path, after validating that the
// bytes deserialize cleanly into an ERunConfig. The validation is the
// whole point of routing the restore through this helper instead of a
// plain cp: a corrupted backup must not replace a (possibly less
// corrupted) live file. Restores via the atomic-write helper so a
// crash mid-restore cannot itself produce a partial file.
func RestoreRootConfigFromBackup(backupPath, livePath string) error {
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
	probe := ERunConfig{}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("backup %s is not a valid erun config: %w", backupPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(livePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}
	if err := writeFileAtomic(livePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

// pruneOldRootConfigBackups enforces the count-based retention
// policy. Backups whose filenames do not parse as YYYY-MM-DD are
// considered out-of-band and are not counted toward the retention
// budget; they are also not removed. This is intentional: the policy
// is "evict by count among files we wrote" rather than "delete any
// .bak in this directory."
func pruneOldRootConfigBackups(dir, base string, keep int) error {
	if keep <= 0 {
		return nil
	}
	backups, err := listManagedRootConfigBackups(dir, base)
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

func listManagedRootConfigBackups(dir, base string) ([]RootConfigBackup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	backups := make([]RootConfigBackup, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		date, ok := parseRootConfigBackupName(base, entry.Name())
		if !ok {
			continue
		}
		backups = append(backups, RootConfigBackup{
			Path: filepath.Join(dir, entry.Name()),
			Date: date,
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Date.After(backups[j].Date)
	})
	return backups, nil
}

func rootConfigBackupName(base string, t time.Time) string {
	return base + "." + t.UTC().Format("2006-01-02") + rootConfigBackupSuffix
}

func parseRootConfigBackupName(base, name string) (time.Time, bool) {
	prefix := base + "."
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	if !strings.HasSuffix(name, rootConfigBackupSuffix) {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, prefix), rootConfigBackupSuffix)
	parsed, err := time.Parse("2006-01-02", stamp)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

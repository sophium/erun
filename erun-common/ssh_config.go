package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var userHomeDir = os.UserHomeDir

type SSHHostEntry struct {
	Alias        string
	HostKeyAlias string
	HostName     string
	Port         int
	User         string
	IdentityFile string
}

func DefaultSSHConfigPath() (string, error) {
	homeDir, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ssh", "config"), nil
}

func UpsertDefaultSSHConfig(entry SSHHostEntry) (string, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return "", err
	}
	return path, UpsertSSHConfig(path, entry)
}

// DefaultSSHConfigHasAlias reports whether the default ssh config (~/.ssh/config)
// already declares a Host block for alias, so a caller can tell an alias name
// that was merely derived from tenant/environment apart from one that will
// actually resolve for an ssh client on this host.
func DefaultSSHConfigHasAlias(alias string) (bool, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return false, err
	}
	return SSHConfigHasAlias(path, alias)
}

func SSHConfigHasAlias(path, alias string) (bool, error) {
	data, err := os.ReadFile(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, line := range splitConfigLines(string(data)) {
		if hostLineHasAlias(line, alias) {
			return true, nil
		}
	}
	return false, nil
}

func UpsertSSHConfig(path string, entry SSHHostEntry) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return fmt.Errorf("ssh config path is required")
	}
	if strings.TrimSpace(entry.Alias) == "" {
		return fmt.Errorf("ssh host alias is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	updated := UpsertSSHConfigContent(string(data), entry)
	return os.WriteFile(path, []byte(updated), 0o600)
}

func UpsertSSHConfigContent(existing string, entry SSHHostEntry) string {
	lines := splitConfigLines(existing)
	updated, replaced := replaceExistingHostEntries(lines, entry)
	if !replaced {
		updated = appendBlankBeforeEntry(updated)
		updated = appendEntryLines(updated, entry)
	}

	return strings.TrimRight(strings.Join(trimTrailingBlankLines(updated), "\n"), "\n") + "\n"
}

// RemoveDefaultSSHConfigAlias drops the Host block for alias from the default
// ssh config, the inverse of UpsertDefaultSSHConfig. It reports whether a block
// was actually removed, so a caller can tell "we had written one" apart from
// "there was nothing of ours to remove".
func RemoveDefaultSSHConfigAlias(alias string) (bool, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return false, err
	}
	return RemoveSSHConfigAlias(path, alias)
}

// RemoveSSHConfigAlias removes every Host block in path that declares exactly
// alias and leaves the rest of the file — other environments' blocks and
// hand-maintained entries alike — intact. A Host line naming several aliases is
// left alone: this removes only a block it is certain belongs to alias alone,
// and the writer emits one alias per block.
func RemoveSSHConfigAlias(path, alias string) (bool, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	alias = strings.TrimSpace(alias)
	if path == "" {
		return false, fmt.Errorf("ssh config path is required")
	}
	if alias == "" {
		return false, fmt.Errorf("ssh host alias is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	updated, removed := RemoveSSHConfigAliasContent(string(data), alias)
	if !removed {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0o600)
}

func RemoveSSHConfigAliasContent(existing, alias string) (string, bool) {
	lines := splitConfigLines(existing)
	updated := make([]string, 0, len(lines))
	removed := false
	for i := 0; i < len(lines); {
		if !hostLineNamesOnlyAlias(lines[i], alias) {
			updated = append(updated, lines[i])
			i++
			continue
		}
		removed = true
		i = skipHostEntry(lines, i+1)
		// Drop the blank separator that immediately followed the block, so
		// removal does not leave a double gap where it used to be.
		if i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
	}
	if !removed {
		return existing, false
	}
	joined := strings.Join(trimTrailingBlankLines(updated), "\n")
	if joined == "" {
		return "", true
	}
	return joined + "\n", true
}

func replaceExistingHostEntries(lines []string, entry SSHHostEntry) ([]string, bool) {
	replaced := false
	updated := make([]string, 0, len(lines)+8)
	for i := 0; i < len(lines); {
		if !hostLineHasAlias(lines[i], entry.Alias) {
			updated = append(updated, lines[i])
			i++
			continue
		}
		updated = appendFirstReplacement(updated, entry, replaced)
		replaced = true
		i = skipHostEntry(lines, i+1)
		updated = appendBlankBetweenEntries(lines, updated, i)
	}
	return updated, replaced
}

func appendFirstReplacement(lines []string, entry SSHHostEntry, replaced bool) []string {
	if replaced {
		return lines
	}
	return appendEntryLines(lines, entry)
}

func skipHostEntry(lines []string, index int) int {
	for index < len(lines) && !isHostDirective(lines[index]) {
		index++
	}
	return index
}

func appendBlankBetweenEntries(source, updated []string, nextIndex int) []string {
	if nextIndex >= len(source) || len(updated) == 0 || strings.TrimSpace(updated[len(updated)-1]) == "" {
		return updated
	}
	return append(updated, "")
}

func appendBlankBeforeEntry(lines []string) []string {
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		return append(lines, "")
	}
	return lines
}

func RenderSSHHostEntry(entry SSHHostEntry) string {
	lines := []string{
		"Host " + entry.Alias,
		"  HostName " + entry.HostName,
		fmt.Sprintf("  Port %d", entry.Port),
		"  User " + entry.User,
	}
	if strings.TrimSpace(entry.HostKeyAlias) != "" {
		lines = append(lines, "  HostKeyAlias "+entry.HostKeyAlias)
	}
	if strings.TrimSpace(entry.IdentityFile) != "" {
		lines = append(lines, "  IdentityFile "+entry.IdentityFile)
	}
	return strings.Join(lines, "\n") + "\n"
}

func appendEntryLines(lines []string, entry SSHHostEntry) []string {
	return append(lines, splitConfigLines(RenderSSHHostEntry(entry))...)
}

func splitConfigLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func trimTrailingBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isHostDirective(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "Host ")
}

func hostLineHasAlias(line, alias string) bool {
	if !isHostDirective(line) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return false
	}
	for _, field := range fields[1:] {
		if field == alias {
			return true
		}
	}
	return false
}

// hostLineNamesOnlyAlias reports whether line is a Host directive whose sole
// alias is alias. Removal uses it rather than hostLineHasAlias so that a shared
// "Host a b" line — never written by UpsertSSHConfig, and possibly another
// environment's only remaining block — is never deleted.
func hostLineNamesOnlyAlias(line, alias string) bool {
	if !isHostDirective(line) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) == 2 && fields[1] == alias
}

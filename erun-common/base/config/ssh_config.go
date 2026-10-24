package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

var userHomeDir = os.UserHomeDir

func DefaultSSHConfigPath() (string, error) {
	homeDir, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ssh", "config"), nil
}

func UpsertDefaultSSHConfig(entry model.SSHHostEntry) (string, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return "", err
	}
	return path, UpsertSSHConfig(path, entry)
}

// DefaultSSHConfigHasAlias reports whether ~/.ssh/config declares a Host block for alias.
func DefaultSSHConfigHasAlias(alias string) (bool, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return false, err
	}
	return SSHConfigHasAlias(path, alias)
}

// ParseSSHHostEntries reads every Host block in file order, one entry per alias.
// Directives erun does not write are ignored.
func ParseSSHHostEntries(content string) []model.SSHHostEntry {
	lines := splitConfigLines(content)
	entries := make([]model.SSHHostEntry, 0, 4)
	var block []*model.SSHHostEntry
	for _, line := range lines {
		if isHostDirective(line) {
			block = block[:0]
			for _, alias := range hostDirectiveAliases(line) {
				entries = append(entries, model.SSHHostEntry{Alias: alias})
				block = append(block, &entries[len(entries)-1])
			}
			continue
		}
		if len(block) == 0 {
			continue
		}
		key, value, ok := splitDirective(line)
		if !ok {
			continue
		}
		for _, entry := range block {
			applySSHHostDirective(entry, key, value)
		}
	}
	return entries
}

func applySSHHostDirective(entry *model.SSHHostEntry, key, value string) {
	switch strings.ToLower(key) {
	case "hostname":
		entry.HostName = value
	case "hostkeyalias":
		entry.HostKeyAlias = value
	case "user":
		entry.User = value
	case "identityfile":
		entry.IdentityFile = value
	case "port":
		if port, err := strconv.Atoi(value); err == nil {
			entry.Port = port
		}
	}
}

// hostDirectiveAliases returns the aliases a Host line declares, skipping negated ("!alias") names.
func hostDirectiveAliases(line string) []string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 {
		return nil
	}
	aliases := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "!") {
			continue
		}
		aliases = append(aliases, field)
	}
	return aliases
}

func splitDirective(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if index := strings.IndexAny(trimmed, " \t="); index >= 0 {
		key = strings.TrimSpace(trimmed[:index])
		value = strings.TrimSpace(strings.TrimPrefix(trimmed[index+1:], "="))
		value = strings.TrimSpace(value)
	} else {
		key = trimmed
	}
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

// ReadDefaultSSHHostEntries parses the default ssh config; a missing file yields no entries.
func ReadDefaultSSHHostEntries() ([]model.SSHHostEntry, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseSSHHostEntries(string(data)), nil
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

func UpsertSSHConfig(path string, entry model.SSHHostEntry) error {
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

func UpsertSSHConfigContent(existing string, entry model.SSHHostEntry) string {
	lines := splitConfigLines(existing)
	updated, replaced := replaceExistingHostEntries(lines, entry)
	if !replaced {
		updated = appendBlankBeforeEntry(updated)
		updated = appendEntryLines(updated, entry)
	}

	return strings.TrimRight(strings.Join(trimTrailingBlankLines(updated), "\n"), "\n") + "\n"
}

// RemoveDefaultSSHConfigAlias drops alias's Host block from the default ssh config
// and reports whether one was removed.
func RemoveDefaultSSHConfigAlias(alias string) (bool, error) {
	path, err := DefaultSSHConfigPath()
	if err != nil {
		return false, err
	}
	return RemoveSSHConfigAlias(path, alias)
}

// RemoveSSHConfigAlias removes every Host block naming exactly alias; shared
// multi-alias Host lines are left alone.
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

func replaceExistingHostEntries(lines []string, entry model.SSHHostEntry) ([]string, bool) {
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

func appendFirstReplacement(lines []string, entry model.SSHHostEntry, replaced bool) []string {
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

func RenderSSHHostEntry(entry model.SSHHostEntry) string {
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

func appendEntryLines(lines []string, entry model.SSHHostEntry) []string {
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

// hostLineNamesOnlyAlias reports whether line is a Host directive whose sole alias is alias.
func hostLineNamesOnlyAlias(line, alias string) bool {
	if !isHostDirective(line) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) == 2 && fields[1] == alias
}

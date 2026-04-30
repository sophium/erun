package sshconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var userHomeDir = os.UserHomeDir

type HostEntry struct {
	Alias        string
	HostKeyAlias string
	HostName     string
	Port         int
	User         string
	IdentityFile string
}

func DefaultConfigPath() (string, error) {
	homeDir, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ssh", "config"), nil
}

func UpsertDefaultConfig(entry HostEntry) (string, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return "", err
	}
	return path, UpsertConfig(path, entry)
}

func UpsertConfig(path string, entry HostEntry) error {
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
	updated := UpsertConfigContent(string(data), entry)
	return os.WriteFile(path, []byte(updated), 0o600)
}

func UpsertConfigContent(existing string, entry HostEntry) string {
	lines := splitConfigLines(existing)
	updated, replaced := replaceExistingHostEntries(lines, entry)
	if !replaced {
		updated = appendBlankBeforeEntry(updated)
		updated = appendEntryLines(updated, entry)
	}

	return strings.TrimRight(strings.Join(trimTrailingBlankLines(updated), "\n"), "\n") + "\n"
}

func replaceExistingHostEntries(lines []string, entry HostEntry) ([]string, bool) {
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

func appendFirstReplacement(lines []string, entry HostEntry, replaced bool) []string {
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

func RenderEntry(entry HostEntry) string {
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

func appendEntryLines(lines []string, entry HostEntry) []string {
	return append(lines, splitConfigLines(RenderEntry(entry))...)
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

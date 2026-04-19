package sshknownhosts

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var userHomeDir = os.UserHomeDir

func DefaultKnownHostsPath() (string, error) {
	homeDir, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ssh", "known_hosts"), nil
}

func UpsertDefaultKnownHost(alias, host string, port int) (string, error) {
	path, err := DefaultKnownHostsPath()
	if err != nil {
		return "", err
	}
	return path, UpsertKnownHost(path, alias, host, port)
}

func UpsertKnownHost(path, alias, host string, port int) error {
	path = filepath.Clean(strings.TrimSpace(path))
	alias = strings.TrimSpace(alias)
	host = strings.TrimSpace(host)
	if path == "" {
		return fmt.Errorf("known_hosts path is required")
	}
	if alias == "" {
		return fmt.Errorf("known_hosts alias is required")
	}
	if host == "" {
		return fmt.Errorf("known_hosts host is required")
	}
	if port <= 0 {
		return fmt.Errorf("known_hosts port is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	scannedLines, err := scanHostKeys(host, port)
	if err != nil {
		return err
	}
	updated := upsertKnownHostsContent(string(data), alias, hostPortToken(host, port), scannedLines)
	return os.WriteFile(path, []byte(updated), 0o600)
}

func scanHostKeys(host string, port int) ([]string, error) {
	cmd := exec.Command("ssh-keyscan", "-p", fmt.Sprintf("%d", port), host)
	output := new(bytes.Buffer)
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()

	lines := make([]string, 0, 3)
	for _, line := range strings.Split(strings.ReplaceAll(output.String(), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		if err != nil {
			return nil, fmt.Errorf("scan ssh host key: %w: %s", err, strings.TrimSpace(output.String()))
		}
		return nil, fmt.Errorf("scan ssh host key: no host keys returned")
	}
	return lines, nil
}

func upsertKnownHostsContent(existing, alias, hostToken string, scannedLines []string) string {
	lines := splitKnownHostsLines(existing)
	filtered := make([]string, 0, len(lines)+(len(scannedLines)*2))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		first, _, _ := strings.Cut(trimmed, " ")
		if first == alias || first == hostToken {
			continue
		}
		filtered = append(filtered, line)
	}

	for _, line := range scannedLines {
		_, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		filtered = append(filtered, hostToken+" "+rest)
		filtered = append(filtered, alias+" "+rest)
	}

	return strings.TrimRight(strings.Join(filtered, "\n"), "\n") + "\n"
}

func splitKnownHostsLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func hostPortToken(host string, port int) string {
	return fmt.Sprintf("[%s]:%d", strings.TrimSpace(host), port)
}

package sshknownhosts

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
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
	cmd := eruncommon.Command("ssh-keyscan", "-p", fmt.Sprintf("%d", port), host)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return parseHostKeyScanOutput(stdout.String(), stderr.String(), err)
}

// parseHostKeyScanOutput extracts host key lines from an ssh-keyscan run. Only
// stdout is a candidate for keys: ssh-keyscan writes diagnostics (KEX/negotiation
// failures) to stderr, and a diagnostic has no "#" prefix, so it would otherwise
// pass the blank/comment filter and be mistaken for a key. Each candidate line is
// further required to name a known host key type, since a scan can also emit
// non-key stdout noise.
func parseHostKeyScanOutput(stdout, stderr string, cmdErr error) ([]string, error) {
	lines := make([]string, 0, 3)
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !isHostKeyLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = strings.TrimSpace(stdout)
		}
		if cmdErr != nil {
			return nil, fmt.Errorf("scan ssh host key: %w: %s", cmdErr, detail)
		}
		if detail != "" {
			return nil, fmt.Errorf("scan ssh host key: no host keys returned: %s", detail)
		}
		return nil, fmt.Errorf("scan ssh host key: no host keys returned")
	}
	return lines, nil
}

// isHostKeyLine reports whether line looks like a "host keytype base64key"
// known_hosts entry rather than diagnostic text such as an unsupported-KEX
// message, which has no key-type token to match against.
func isHostKeyLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return false
	}
	return isKnownHostKeyType(fields[1])
}

func isKnownHostKeyType(keyType string) bool {
	switch keyType {
	case "ssh-rsa", "ssh-dss", "ssh-ed25519":
		return true
	}
	return strings.HasPrefix(keyType, "ecdsa-sha2-") || strings.HasPrefix(keyType, "sk-")
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

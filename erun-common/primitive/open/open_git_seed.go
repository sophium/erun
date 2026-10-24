package open

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func seedableGitRemote(dir string) (host, user, repo string, ok bool) {
	host, user, repo, err := resolveGitRemote(dir)
	return host, user, repo, err == nil
}

func remoteShellGitSeedLines(req configpkg.ShellLaunchParams, workdir string) ([]string, error) {
	if req.RemoteRepo {
		return nil, nil
	}
	gitHost, gitUser, gitRepo, ok := seedableGitRemote(req.Dir)
	if !ok {
		return nil, nil
	}
	knownHostsLines, err := loadKnownHostsLines(gitHost)
	if err != nil {
		return nil, err
	}
	// The private key goes over stdin (seedRemoteSSHKey), never this script.
	return RemoteShellGitSeedScriptLines(workdir, gitHost, core.ShellQuote(gitUser), core.ShellQuote(gitRepo), strings.Join(knownHostsLines, "\n")), nil
}

func RemoteShellGitSeedScriptLines(workdir, gitHost, gitUser, gitRepo, knownHosts string) []string {
	sshConfig := strings.Join([]string{
		fmt.Sprintf("Host %s", gitHost),
		fmt.Sprintf("  HostName %s", gitHost),
		"  IdentityFile ~/.ssh/keys",
		"  IdentitiesOnly yes",
		"  UserKnownHostsFile ~/.ssh/known_hosts",
	}, "\n")
	return []string{
		"set -eu",
		"mkdir -p \"$HOME/.ssh\"",
		"chmod 700 \"$HOME/.ssh\"",
		// Do not touch ~/.ssh/keys here — seedRemoteSSHKey owns it (stdin).
		"rm -f \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/config\"",
		"old_umask=\"$(umask)\"",
		"umask 077",
		fmt.Sprintf("cat > \"$HOME/.ssh/known_hosts\" <<'EOF'\n%s\nEOF", knownHosts),
		fmt.Sprintf("cat > \"$HOME/.ssh/config\" <<'EOF'\n%s\nEOF", sshConfig),
		"umask \"$old_umask\"",
		"chmod 600 \"$HOME/.ssh/known_hosts\" \"$HOME/.ssh/config\"",
		fmt.Sprintf("mkdir -p %s", workdir),
		fmt.Sprintf("cd %s", workdir),
		// Concurrent opens race on ~/.gitconfig.lock; the guard and `|| true`
		// keep the loser from aborting under `set -eu`.
		fmt.Sprintf("if command -v git >/dev/null 2>&1; then if [ ! -d .git ]; then git clone git@%s:%s/%s.git .; fi; if ! git config --global --get-all safe.directory 2>/dev/null | grep -qFx '*'; then git config --global --add safe.directory '*' 2>/dev/null || true; fi; fi", gitHost, gitUser, gitRepo),
	}
}

func resolveGitRemote(repoPath string) (string, string, string, error) {
	output, err := core.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", "", err
	}

	remote := strings.TrimSpace(string(output))
	switch {
	case strings.HasPrefix(remote, "git@"):
		hostRepo := strings.TrimPrefix(remote, "git@")
		host, repoPath, ok := strings.Cut(hostRepo, ":")
		if !ok {
			return "", "", "", fmt.Errorf("unexpected git remote %q", remote)
		}
		user, repo := splitRepoPath(repoPath)
		return host, user, repo, nil
	case strings.HasPrefix(remote, "ssh://"):
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", "", "", err
		}
		repoPath := strings.TrimPrefix(parsed.Path, "/")
		user, repo := splitRepoPath(repoPath)
		return parsed.Hostname(), user, repo, nil
	default:
		return "", "", "", fmt.Errorf("unsupported git remote %q", remote)
	}
}

func splitRepoPath(repoPath string) (string, string) {
	repoPath = strings.TrimSuffix(repoPath, ".git")
	return path.Dir(repoPath), path.Base(repoPath)
}

func resolveSSHConfigEntries(host string) ([]sshConfigEntry, error) {
	sshConfigPath := filepath.Join(resolveOpenUserHomeDir(), ".ssh", "config")
	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	entries := parseSSHConfig(string(data))
	matches := make([]sshConfigEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.matchesHost(host) {
			matches = append(matches, entry)
		}
	}
	return matches, nil
}

func loadKnownHostsLines(host string) ([]string, error) {
	knownHostsPath := filepath.Join(resolveOpenUserHomeDir(), ".ssh", "known_hosts")
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	lines := make([]string, 0, 4)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, host) {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(lines)
	return lines, nil
}

// loadPrivateKeyMaterial needs no redaction: the material only travels on stdin.
func loadPrivateKeyMaterial(entries []sshConfigEntry) ([]string, error) {
	keyPaths := privateKeyPaths(entries)

	lines := make([]string, 0, len(keyPaths))
	for _, keyPath := range keyPaths {
		data, err := os.ReadFile(keyPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		lines = append(lines, string(data))
	}
	return lines, nil
}

const remoteSSHKeySeedScript = `umask 077; mkdir -p "$HOME/.ssh"; cat > "$HOME/.ssh/keys"; chmod 600 "$HOME/.ssh/keys"`

func remoteShellPrivateKeyMaterial(req configpkg.ShellLaunchParams) (string, error) {
	if req.RemoteRepo {
		return "", nil
	}
	gitHost, _, _, ok := seedableGitRemote(req.Dir)
	if !ok {
		return "", nil
	}
	entries, err := resolveSSHConfigEntries(gitHost)
	if err != nil {
		return "", err
	}
	keyLines, err := loadPrivateKeyMaterial(entries)
	if err != nil {
		return "", err
	}
	return strings.Join(keyLines, "\n"), nil
}

// RemoteSSHKeySeedArgs uses -i (not -it) so stdin stays a pipe for the key.
func RemoteSSHKeySeedArgs(req configpkg.ShellLaunchParams) []string {
	args := configpkg.KubectlTargetArgs(req)
	return append(args, "exec", "-i", "deployment/"+configpkg.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-c", remoteSSHKeySeedScript)
}

func seedRemoteSSHKey(req configpkg.ShellLaunchParams) error {
	keyMaterial, err := remoteShellPrivateKeyMaterial(req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(keyMaterial) == "" {
		return nil
	}
	cmd := core.Command("kubectl", RemoteSSHKeySeedArgs(req)...)
	cmd.Stdin = strings.NewReader(keyMaterial)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(output.String()); message != "" {
			return fmt.Errorf("seed remote ssh key: %w: %s", err, message)
		}
		return fmt.Errorf("seed remote ssh key: %w", err)
	}
	return nil
}

func privateKeyPaths(entries []sshConfigEntry) []string {
	keyPaths := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for _, entry := range entries {
		for _, identityFile := range entry.identityFiles {
			keyPaths = appendUniquePrivateKeyPath(keyPaths, seen, identityFile)
		}
	}
	if len(keyPaths) > 0 {
		return keyPaths
	}
	for _, fallback := range []string{"id_rsa", "id_ed25519", "id_ecdsa"} {
		keyPaths = appendUniquePrivateKeyPath(keyPaths, seen, filepath.Join(resolveOpenUserHomeDir(), ".ssh", fallback))
	}
	return keyPaths
}

func appendUniquePrivateKeyPath(keyPaths []string, seen map[string]struct{}, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return keyPaths
	}
	if _, ok := seen[path]; ok {
		return keyPaths
	}
	seen[path] = struct{}{}
	return append(keyPaths, path)
}

type sshConfigEntry struct {
	patterns      []string
	identityFiles []string
}

func (e sshConfigEntry) matchesHost(host string) bool {
	for _, pattern := range e.patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		ok, err := path.Match(pattern, host)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func parseSSHConfig(contents string) []sshConfigEntry {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	entries := make([]sshConfigEntry, 0, 4)
	current := sshConfigEntry{}

	flush := func() {
		if len(current.patterns) == 0 {
			return
		}
		entries = append(entries, current)
		current = sshConfigEntry{}
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		switch strings.ToLower(fields[0]) {
		case "host":
			flush()
			current.patterns = append(current.patterns, fields[1:]...)
		case "identityfile":
			current.identityFiles = append(current.identityFiles, expandSSHPath(fields[1]))
		}
	}
	flush()
	return entries
}

func expandSSHPath(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(resolveOpenUserHomeDir(), strings.TrimPrefix(value, "~/"))
	}
	return value
}

func resolveOpenUserHomeDir() string {
	homeDir, err := openUserHomeDir()
	if err == nil && strings.TrimSpace(homeDir) != "" {
		return homeDir
	}
	return strings.TrimSpace(os.Getenv("HOME"))
}

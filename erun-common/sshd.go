package eruncommon

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	DefaultSSHUser      = "erun"
	DefaultSSHLocalPort = 62222
	RemoteSSHDPort      = 2222
)

type SSHConnectionInfo struct {
	User          string
	Host          string
	Port          int
	WorkspacePath string
	HostAlias     string
}

func SSHConnectionInfoForResult(result OpenResult) SSHConnectionInfo {
	req := ShellLaunchParamsFromResult(result)
	return SSHConnectionInfo{
		User:          DefaultSSHUser,
		Host:          "127.0.0.1",
		Port:          result.EnvConfig.SSHD.ResolvedLocalPort(),
		WorkspacePath: RemoteShellWorktreePath(req),
		HostAlias:     SSHHostAlias(result.Tenant, result.Environment),
	}
}

var sshHostAliasSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func SSHHostAlias(tenant, environment string) string {
	parts := []string{"erun", sanitizeSSHHostAliasToken(tenant), sanitizeSSHHostAliasToken(environment)}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return "erun"
	}
	return strings.Join(filtered, "-")
}

func SSHPrivateKeyPath(publicKeyPath string) string {
	publicKeyPath = strings.TrimSpace(publicKeyPath)
	if publicKeyPath == "" {
		return ""
	}
	if strings.HasSuffix(publicKeyPath, ".pub") {
		return strings.TrimSuffix(filepath.Clean(publicKeyPath), ".pub")
	}
	return ""
}

func sanitizeSSHHostAliasToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = sshHostAliasSanitizer.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	return value
}

func ValidateSSHDTarget(result OpenResult) error {
	if !result.RemoteRepo() {
		return fmt.Errorf("sshd requires a remote environment initialized with --remote")
	}
	if strings.TrimSpace(result.EnvConfig.KubernetesContext) == "" {
		return fmt.Errorf("%w: %s/%s", ErrKubernetesContextNotConfigured, result.Tenant, result.Environment)
	}
	return nil
}

func BuildRemoteAuthorizedKeysSyncScript(publicKey string) string {
	publicKey = strings.TrimSpace(publicKey)
	return strings.Join([]string{
		"set -eu",
		"mkdir -p \"$HOME/.ssh\"",
		"chmod 700 \"$HOME/.ssh\"",
		"key_file=\"$HOME/.ssh/authorized_keys\"",
		"tmp_keys=\"$(mktemp)\"",
		"tmp_new=\"$(mktemp)\"",
		"touch \"$key_file\"",
		"chmod 600 \"$key_file\"",
		"cat > \"$tmp_new\" <<'EOF'\n" + publicKey + "\nEOF",
		"grep -Fvx -f \"$tmp_new\" \"$key_file\" > \"$tmp_keys\" || true",
		"cat \"$tmp_new\" >> \"$tmp_keys\"",
		"mv \"$tmp_keys\" \"$key_file\"",
		"rm -f \"$tmp_new\"",
		"chmod 600 \"$key_file\"",
	}, "\n")
}

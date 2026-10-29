package sshconn

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/shelllaunch"
	"github.com/sophium/erun/erun-common/primitive/open/runtimeref"
)

const DefaultSSHUser = "erun"

// SSHHostAliasPrefix distinguishes erun's aliases from hand-maintained Host blocks.
const (
	SSHHostAliasRoot   = "erun"
	SSHHostAliasPrefix = SSHHostAliasRoot + "-"
)

func SSHConnectionInfoForResult(result config.OpenResult) model.SSHConnectionInfo {
	req := shelllaunch.ShellLaunchParamsFromResult(result)
	return model.SSHConnectionInfo{
		User:           DefaultSSHUser,
		Host:           "127.0.0.1",
		Port:           localports.SSHLocalPortForResult(result),
		WorkspacePath:  runtimeref.RemoteShellWorktreePath(req),
		HostAlias:      SSHHostAlias(result.Tenant, result.Environment),
		PrivateKeyPath: SSHPrivateKeyPath(result.EnvConfig.SSHD.PublicKeyPath),
	}
}

var sshHostAliasSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func SSHHostAlias(tenant, environment string) string {
	parts := []string{SSHHostAliasRoot, sanitizeSSHHostAliasToken(tenant), sanitizeSSHHostAliasToken(environment)}
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

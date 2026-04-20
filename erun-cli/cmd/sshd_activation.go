package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

type SSHDActivator func(common.Context, common.OpenResult) error

func newSSHDActivator(runRemoteCommand common.RemoteCommandRunnerFunc) SSHDActivator {
	return func(ctx common.Context, result common.OpenResult) error {
		if !result.EnvConfig.SSHD.Enabled {
			return nil
		}
		if err := common.ValidateSSHDTarget(result); err != nil {
			return err
		}

		if _, err := syncRemoteSSHDKey(ctx, result, runRemoteCommand); err != nil {
			return err
		}

		info, err := ensureSSHDPortForward(ctx, result)
		if err != nil {
			return err
		}
		return emitSSHDConnectionInfo(ctx, info)
	}
}

func syncRemoteSSHDKey(ctx common.Context, result common.OpenResult, runRemoteCommand common.RemoteCommandRunnerFunc) (string, error) {
	publicKeyPath, publicKey, err := resolveSSHDPublicKey(result.EnvConfig.SSHD.PublicKeyPath)
	if err != nil {
		return "", err
	}
	req := common.ShellLaunchParamsFromResult(result)
	script := common.BuildRemoteAuthorizedKeysSyncScript(publicKey)
	output, err := common.RunTracedRemoteCommand(ctx, runRemoteCommand, req, "remote-ssh-authorized-keys", script)
	if err != nil {
		return "", fmt.Errorf("sync remote authorized_keys from %s: %w%s", publicKeyPath, err, formatRemoteCommandStderr(output.Stderr))
	}
	return publicKeyPath, nil
}

func resolveSSHDPublicKey(path string) (string, string, error) {
	if strings.TrimSpace(path) != "" {
		publicKey, err := readSSHDPublicKey(path)
		if err != nil {
			return "", "", err
		}
		return filepath.Clean(path), publicKey, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	candidates := []string{
		filepath.Join(homeDir, ".ssh", "id_ed25519.pub"),
		filepath.Join(homeDir, ".ssh", "id_ecdsa.pub"),
		filepath.Join(homeDir, ".ssh", "id_rsa.pub"),
	}
	for _, candidate := range candidates {
		publicKey, err := readSSHDPublicKey(candidate)
		if err == nil {
			return candidate, publicKey, nil
		}
		if !os.IsNotExist(err) {
			return "", "", err
		}
	}

	return "", "", fmt.Errorf("no SSH public key found; use --public-key to choose one")
}

func readSSHDPublicKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	publicKey := strings.TrimSpace(string(data))
	if publicKey == "" {
		return "", fmt.Errorf("SSH public key is empty: %s", path)
	}
	return publicKey, nil
}

func emitSSHDConnectionInfo(ctx common.Context, info common.SSHConnectionInfo) error {
	if ctx.Stdout == nil {
		return nil
	}
	_, err := fmt.Fprintf(
		ctx.Stdout,
		"SSH:\n  host: %s\n  alias: %s\n  port: %d\n  user: %s\n  key: %s\n  workspace: %s\n",
		info.Host,
		info.HostAlias,
		info.Port,
		info.User,
		valueOrNone(info.PrivateKeyPath),
		info.WorkspacePath,
	)
	return err
}

func formatRemoteCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

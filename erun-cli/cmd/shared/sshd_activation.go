package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

type SSHDActivator func(core.Context, config.OpenResult) error

var (
	waitForSSHDRemoteDeployment    = common.WaitForShellDeployment
	sleepBeforeSSHDRemoteExecRetry = time.Sleep
)

const (
	sshdRemoteExecMaxAttempts = 3
	sshdRemoteExecRetryDelay  = 5 * time.Second
)

func SyncRemoteSSHDKey(ctx core.Context, result config.OpenResult, runRemoteCommand kube.RemoteCommandRunnerFunc) (string, error) {
	publicKeyPath, publicKey, err := ResolveSSHDPublicKey(result.EnvConfig.SSHD.PublicKeyPath)
	if err != nil {
		if !ctx.DryRun {
			return "", err
		}
		ctx.Trace("sshd: dry-run: " + err.Error() + "; using placeholder key for trace")
		publicKeyPath = "<no-public-key>"
		publicKey = "<placeholder-public-key>"
	}
	req := common.ShellLaunchParamsFromResult(result)
	script := common.BuildRemoteAuthorizedKeysSyncScript(publicKey)
	for attempt := 1; attempt <= sshdRemoteExecMaxAttempts; attempt++ {
		output, err := kube.RunTracedRemoteCommand(ctx, runRemoteCommand, req, "remote-ssh-authorized-keys", script)
		if err == nil {
			return publicKeyPath, nil
		}
		if attempt >= sshdRemoteExecMaxAttempts || !sshdRemoteExecNeedsDeploymentRetry(output.Stderr) {
			return "", fmt.Errorf("sync remote authorized_keys from %s: %w%s", publicKeyPath, err, formatRemoteCommandStderr(output.Stderr))
		}
		ctx.Trace("runtime pod/container was not ready while syncing SSH authorized_keys; waiting for deployment and retrying")
		if waitErr := waitForSSHDRemoteDeployment(req); waitErr != nil {
			return "", fmt.Errorf("wait for runtime deployment after SSH authorized_keys sync failed: %w", waitErr)
		}
		sleepBeforeSSHDRemoteExecRetry(sshdRemoteExecRetryDelay)
	}
	return "", fmt.Errorf("sync remote authorized_keys from %s: exceeded retry attempts", publicKeyPath)
}

func sshdRemoteExecNeedsDeploymentRetry(stderr string) bool {
	value := strings.ToLower(stderr)
	return strings.Contains(value, "pod does not exist") ||
		strings.Contains(value, "pod not found") ||
		strings.Contains(value, "lost connection to pod") ||
		strings.Contains(value, "container not found") ||
		strings.Contains(value, "502 bad gateway")
}

func ResolveSSHDPublicKey(path string) (string, string, error) {
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

func formatRemoteCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

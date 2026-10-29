package sshdsetup

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared/opentarget"
)

func ValidateSSHDTarget(result config.OpenResult) error {
	if !result.RemoteRepo() {
		return fmt.Errorf("sshd requires a remote environment initialized with --remote")
	}
	if strings.TrimSpace(result.EnvConfig.KubernetesContext) == "" {
		return fmt.Errorf("%w: %s/%s", opentarget.ErrKubernetesContextNotConfigured, result.Tenant, result.Environment)
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

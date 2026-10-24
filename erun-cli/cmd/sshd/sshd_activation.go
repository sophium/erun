package sshd

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/cmd/shared/sshdlink"
	"github.com/sophium/erun/cmd/shared/textfmt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
)

func NewSSHDActivator(runRemoteCommand kube.RemoteCommandRunnerFunc) sshdlink.SSHDActivator {
	return func(ctx core.Context, result config.OpenResult) error {
		if !result.EnvConfig.SSHD.Enabled {
			return nil
		}
		if err := open.ValidateSSHDTarget(result); err != nil {
			return err
		}

		if _, err := sshdlink.SyncRemoteSSHDKey(ctx, result, runRemoteCommand); err != nil {
			return err
		}

		info, err := sshdlink.EnsureSSHDPortForward(ctx, result)
		if err != nil {
			return err
		}
		return emitSSHDConnectionInfo(ctx, info)
	}
}

// emitSSHDConnectionInfo prints connection details on a terminal only: under --no-shell
// stdout is eval'd, and stderr stays quiet by design.
func emitSSHDConnectionInfo(ctx core.Context, info model.SSHConnectionInfo) error {
	if ctx.Stdout == nil || !prompt.WriterIsTerminal(ctx.Stdout) {
		return nil
	}
	_, err := fmt.Fprintf(
		ctx.Stdout,
		"SSH:\n  host: %s\n  alias: %s\n  port: %d\n  user: %s\n  key: %s\n  workspace: %s\n",
		info.Host,
		info.HostAlias,
		info.Port,
		info.User,
		textfmt.ValueOrNone(info.PrivateKeyPath),
		info.WorkspacePath,
	)
	return err
}

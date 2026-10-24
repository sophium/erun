package sshd

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
)

func NewSSHDActivator(runRemoteCommand kube.RemoteCommandRunnerFunc) shared.SSHDActivator {
	return func(ctx core.Context, result config.OpenResult) error {
		if !result.EnvConfig.SSHD.Enabled {
			return nil
		}
		if err := open.ValidateSSHDTarget(result); err != nil {
			return err
		}

		if _, err := shared.SyncRemoteSSHDKey(ctx, result, runRemoteCommand); err != nil {
			return err
		}

		info, err := shared.EnsureSSHDPortForward(ctx, result)
		if err != nil {
			return err
		}
		return emitSSHDConnectionInfo(ctx, info)
	}
}

// emitSSHDConnectionInfo prints connection details on a terminal only: under --no-shell
// stdout is eval'd, and stderr stays quiet by design.
func emitSSHDConnectionInfo(ctx core.Context, info model.SSHConnectionInfo) error {
	if ctx.Stdout == nil || !shared.WriterIsTerminal(ctx.Stdout) {
		return nil
	}
	_, err := fmt.Fprintf(
		ctx.Stdout,
		"SSH:\n  host: %s\n  alias: %s\n  port: %d\n  user: %s\n  key: %s\n  workspace: %s\n",
		info.Host,
		info.HostAlias,
		info.Port,
		info.User,
		shared.ValueOrNone(info.PrivateKeyPath),
		info.WorkspacePath,
	)
	return err
}

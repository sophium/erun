package sshd

import (
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

func NewSSHDActivator(runRemoteCommand kube.RemoteCommandRunnerFunc) shared.SSHDActivator {
	return func(ctx core.Context, result config.OpenResult) error {
		if !result.EnvConfig.SSHD.Enabled {
			return nil
		}
		if err := common.ValidateSSHDTarget(result); err != nil {
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

// emitSSHDConnectionInfo prints the connection details for a human who ran
// `open` at the terminal, so it is terminal-only. With --no-shell, stdout is
// the shell script the documented `eval "$(erun open ... --no-shell)"` alias
// executes, and this block is prose (`SSH:`, `host:`, ...) that eval would run
// as commands. stderr is not the fallback: the alias does not capture it, and
// keeping it quiet there is deliberate, so a piped run simply omits the block.
func emitSSHDConnectionInfo(ctx core.Context, info common.SSHConnectionInfo) error {
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

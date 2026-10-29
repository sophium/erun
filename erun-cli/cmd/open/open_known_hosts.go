package open

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/open/sshconn"
	sshknownhosts "github.com/sophium/erun/internal/sshknownhosts"
)

var ensureLocalSSHDKnownHostFunc = ensureLocalSSHDKnownHost

func ensureLocalSSHDKnownHost(ctx core.Context, result config.OpenResult) error {
	info := sshconn.SSHConnectionInfoForResult(result)
	ctx.TraceCommand("", "ssh-keyscan", "-p", fmt.Sprintf("%d", info.Port), info.Host)
	if ctx.DryRun {
		return nil
	}
	if _, err := sshknownhosts.UpsertDefaultKnownHost(info.HostAlias, info.Host, info.Port); err != nil {
		return fmt.Errorf("update local known_hosts: %w", err)
	}
	return nil
}

package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	sshknownhosts "github.com/sophium/erun/internal/sshknownhosts"
)

var ensureLocalSSHDKnownHostFunc = ensureLocalSSHDKnownHost

func ensureLocalSSHDKnownHost(ctx common.Context, result common.OpenResult) error {
	info := common.SSHConnectionInfoForResult(result)
	ctx.TraceCommand("", "ssh-keyscan", "-p", fmt.Sprintf("%d", info.Port), info.Host)
	if ctx.DryRun {
		return nil
	}
	if _, err := sshknownhosts.UpsertDefaultKnownHost(info.HostAlias, info.Host, info.Port); err != nil {
		return fmt.Errorf("update local known_hosts: %w", err)
	}
	return nil
}

package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	sshconfig "github.com/sophium/erun/internal/sshconfig"
)

type SSHDLocalConfigResult struct {
	HostAlias  string
	ConfigPath string
}

type SSHDLocalConfigWriter func(common.OpenResult) (SSHDLocalConfigResult, error)

func writeLocalSSHConfig(result common.OpenResult) (SSHDLocalConfigResult, error) {
	info := common.SSHConnectionInfoForResult(result)
	path, err := sshconfig.UpsertDefaultConfig(sshconfig.HostEntry{
		Alias:        info.HostAlias,
		HostKeyAlias: info.HostAlias,
		HostName:     info.Host,
		Port:         info.Port,
		User:         info.User,
		IdentityFile: common.SSHPrivateKeyPath(result.EnvConfig.SSHD.PublicKeyPath),
	})
	if err != nil {
		return SSHDLocalConfigResult{}, fmt.Errorf("write local ssh config: %w", err)
	}
	return SSHDLocalConfigResult{
		HostAlias:  info.HostAlias,
		ConfigPath: path,
	}, nil
}

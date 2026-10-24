package sshdlink

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open"
)

type SSHDLocalConfigResult struct {
	HostAlias  string
	ConfigPath string
}

func WriteLocalSSHConfig(result config.OpenResult) (SSHDLocalConfigResult, error) {
	info := open.SSHConnectionInfoForResult(result)
	path, err := config.UpsertDefaultSSHConfig(model.SSHHostEntry{
		Alias:        info.HostAlias,
		HostKeyAlias: info.HostAlias,
		HostName:     info.Host,
		Port:         info.Port,
		User:         info.User,
		IdentityFile: open.SSHPrivateKeyPath(result.EnvConfig.SSHD.PublicKeyPath),
	})
	if err != nil {
		return SSHDLocalConfigResult{}, fmt.Errorf("write local ssh config: %w", err)
	}
	return SSHDLocalConfigResult{
		HostAlias:  info.HostAlias,
		ConfigPath: path,
	}, nil
}

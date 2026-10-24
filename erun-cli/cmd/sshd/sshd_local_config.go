package sshd

import (
	"github.com/sophium/erun/cmd/shared/sshdlink"
	"github.com/sophium/erun/erun-common/base/config"
)

type SSHDLocalConfigWriter func(config.OpenResult) (sshdlink.SSHDLocalConfigResult, error)

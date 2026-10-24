package sshd

import (
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
)

type SSHDLocalConfigWriter func(config.OpenResult) (shared.SSHDLocalConfigResult, error)

package seam

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type (
	RuntimeContainerCommandRunnerFunc func(config.ShellLaunchParams, string, string) (model.RemoteCommandResult, error)
)

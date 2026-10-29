package runtimepod

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

type (
	RemoteRuntimeWaitFunc func(configpkg.ShellLaunchParams) error
)

type (
	RemoteCommandRunnerFunc func(configpkg.ShellLaunchParams, string) (model.RemoteCommandResult, error)
)

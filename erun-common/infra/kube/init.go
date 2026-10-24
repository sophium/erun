package kube

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type (
	RemoteCommandRunnerFunc func(configpkg.ShellLaunchParams, string) (RemoteCommandResult, error)
)

type (
	RemoteCommandResult struct {
		Stdout string
		Stderr string
	}
)

package seam

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type ShellLauncherFunc func(configpkg.ShellLaunchParams) error

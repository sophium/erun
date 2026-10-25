package runtimepod

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

// RuntimeOutputsRunner runs a /bin/sh script in the env's runtime pod.
type RuntimeOutputsRunner func(req config.ShellLaunchParams, script string) (model.RemoteCommandResult, error)

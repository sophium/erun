package runtimewait

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

const (
	DefaultShellLaunchWaitTimeout = "2m0s"
)

func KubectlDeploymentWaitArgs(req configpkg.ShellLaunchParams) []string {
	args := configpkg.KubectlTargetArgs(req)
	args = append(args, "wait", "--for=condition=Available", "--timeout", DefaultShellLaunchWaitTimeout, "deployment/"+configpkg.RuntimeReleaseName(req.Tenant))
	return args
}

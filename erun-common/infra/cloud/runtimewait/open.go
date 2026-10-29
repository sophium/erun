package runtimewait

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
)

const (
	DefaultShellLaunchWaitTimeout = "2m0s"
)

func KubectlDeploymentWaitArgs(req configpkg.ShellLaunchParams) []string {
	args := runtimetarget.KubectlTargetArgs(req)
	args = append(args, "wait", "--for=condition=Available", "--timeout", DefaultShellLaunchWaitTimeout, "deployment/"+runtimetarget.RuntimeReleaseName(req.Tenant))
	return args
}

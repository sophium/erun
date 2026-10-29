package runtimetarget

import (
	"errors"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

var ErrEnvironmentNotFound = errors.New("no such environment exists")

func RuntimeReleaseName(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return DevopsComponentName
	}
	return tenant + "-devops"
}

func KubectlTargetArgs(req config.ShellLaunchParams) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--context", req.KubernetesContext)
	}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	return args
}

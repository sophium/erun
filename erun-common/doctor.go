package eruncommon

import (
	"bytes"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

type (
	RuntimeContainerCommandRunnerFunc func(config.ShellLaunchParams, string, string) (kube.RemoteCommandResult, error)
)

func ResolveDoctorTarget(store config.OpenStore, params shared.OpenParams) (config.OpenResult, error) {
	return open.ResolveOpen(store, params)
}

func PreviewRuntimeContainerCommand(req config.ShellLaunchParams, container, script string) kube.RemoteCommandPreview {
	return kube.RemoteCommandPreview{
		Args:   kubectlContainerExecArgs(req, container, script),
		Script: script,
	}
}

func RunTracedRuntimeContainerCommand(ctx core.Context, runner RuntimeContainerCommandRunnerFunc, req config.ShellLaunchParams, container, label, script string) (kube.RemoteCommandResult, error) {
	preview := PreviewRuntimeContainerCommand(req, container, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	if ctx.DryRun {
		return kube.RemoteCommandResult{}, nil
	}
	if runner == nil {
		runner = RunRuntimeContainerCommand
	}
	result, err := runner(req, container, script)
	if err != nil {
		return result, cloud.NormalizeDoctorKubectlError(req, result.Stderr, err)
	}
	return result, nil
}

func RunRuntimeContainerCommand(req config.ShellLaunchParams, container, script string) (kube.RemoteCommandResult, error) {
	cmd := core.Command("kubectl", kubectlContainerExecArgs(req, container, script)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return kube.RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func kubectlContainerExecArgs(req config.ShellLaunchParams, container, script string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "exec", "-c", strings.TrimSpace(container))
	args = append(args, "deployment/"+config.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

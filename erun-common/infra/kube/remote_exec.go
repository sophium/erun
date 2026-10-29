package kube

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/runtimepod"
)

type RemoteCommandPreview struct {
	Args   []string
	Script string
}

func PreviewRemoteCommand(req config.ShellLaunchParams, script string) RemoteCommandPreview {
	return RemoteCommandPreview{
		Args:   KubectlRemoteExecArgs(req, script),
		Script: script,
	}
}

// TraceRemoteCommand redacts the script from the argv line; TraceBlock shows it in full.
func TraceRemoteCommand(ctx core.Context, req config.ShellLaunchParams, label, script string) {
	preview := PreviewRemoteCommand(req, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
}

func RunTracedRemoteCommand(ctx core.Context, runner runtimepod.RemoteCommandRunnerFunc, req config.ShellLaunchParams, label, script string) (model.RemoteCommandResult, error) {
	TraceRemoteCommand(ctx, req, label, script)
	if ctx.DryRun {
		return model.RemoteCommandResult{}, nil
	}
	if runner == nil {
		runner = RunRemoteCommand
	}
	return runner(req, script)
}

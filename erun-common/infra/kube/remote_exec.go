package kube

import (
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
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

// TraceRemoteCommand renders the kubectl exec invocation and its script body
// to the audit trace, with the script itself redacted from the argv line (it
// still appears in full via TraceBlock) so a multi-line script doesn't blow
// out the single-line command trace.
func TraceRemoteCommand(ctx core.Context, req config.ShellLaunchParams, label, script string) {
	preview := PreviewRemoteCommand(req, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
}

func RunTracedRemoteCommand(ctx core.Context, runner RemoteCommandRunnerFunc, req config.ShellLaunchParams, label, script string) (RemoteCommandResult, error) {
	TraceRemoteCommand(ctx, req, label, script)
	if ctx.DryRun {
		return RemoteCommandResult{}, nil
	}
	if runner == nil {
		runner = RunRemoteCommand
	}
	return runner(req, script)
}

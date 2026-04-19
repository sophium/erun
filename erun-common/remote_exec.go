package eruncommon

type RemoteCommandPreview struct {
	Args   []string
	Script string
}

func PreviewRemoteCommand(req ShellLaunchParams, script string) RemoteCommandPreview {
	return RemoteCommandPreview{
		Args:   kubectlRemoteExecArgs(req, script),
		Script: script,
	}
}

func RunTracedRemoteCommand(ctx Context, runner RemoteCommandRunnerFunc, req ShellLaunchParams, label, script string) (RemoteCommandResult, error) {
	preview := PreviewRemoteCommand(req, script)
	traceArgs := append([]string{}, preview.Args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	if ctx.DryRun {
		return RemoteCommandResult{}, nil
	}
	if runner == nil {
		runner = RunRemoteCommand
	}
	return runner(req, script)
}

package kube

import (
	"bytes"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func KubectlRemoteExecArgs(req config.ShellLaunchParams, script string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "exec")
	args = append(args, "deployment/"+config.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
	return args
}

func RunRemoteCommand(req config.ShellLaunchParams, script string) (RemoteCommandResult, error) {
	cmd := core.Command("kubectl", KubectlRemoteExecArgs(req, script)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}

func FormatRemoteCommandStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}

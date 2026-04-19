package cmd

import (
	"fmt"
	"net/url"
	"os/exec"

	common "github.com/sophium/erun/erun-common"
)

var vscodeExecCommand = exec.Command

type VSCodeLauncher func(common.Context, common.OpenResult) error

func launchVSCode(ctx common.Context, result common.OpenResult) error {
	if err := ensureLocalSSHDKnownHostFunc(ctx, result); err != nil {
		return err
	}
	command, args, err := vscodeLaunchCommand(currentHostOS(), vscodeRemoteFolderURI(result))
	if err != nil {
		return err
	}
	ctx.TraceCommand("", command, args...)
	if ctx.DryRun {
		return nil
	}

	cmd := vscodeExecCommand(command, args...)
	cmd.Stdin = ctx.Stdin
	cmd.Stdout = ctx.Stdout
	cmd.Stderr = ctx.Stderr
	return cmd.Run()
}

func vscodeRemoteFolderURI(result common.OpenResult) string {
	info := common.SSHConnectionInfoForResult(result)
	return "vscode://vscode-remote/ssh-remote+" + url.PathEscape(info.HostAlias) + (&url.URL{Path: info.WorkspacePath}).EscapedPath()
}

func vscodeLaunchCommand(hostOS common.HostOS, uri string) (string, []string, error) {
	switch hostOS {
	case common.HostOSDarwin:
		return "open", []string{uri}, nil
	case common.HostOSLinux:
		return "xdg-open", []string{uri}, nil
	case common.HostOSWindows:
		return "cmd", []string{"/c", "start", "", uri}, nil
	default:
		return "", nil, fmt.Errorf("opening VS Code is unsupported on %s", hostOS)
	}
}

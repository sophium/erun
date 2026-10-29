package open

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared/desktop"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/sshconn"
)

var (
	ideExecCommand                   = exec.Command
	ideLookPath                      = exec.LookPath
	ideSleep                         = time.Sleep
	ideReadFile                      = os.ReadFile
	ideWriteFile                     = os.WriteFile
	ideMkdirAll                      = os.MkdirAll
	ideOpenURIFunc                   = openIDEURICommand
	ideStartURIFunc                  = startIDEURICommand
	runJetBrainsBootstrapAttemptFunc = runJetBrainsBootstrapAttempt
	openInstalledIntelliJAppFunc     = openInstalledIntelliJApp
	openIntelliJGatewayProjectFunc   = openIntelliJGatewayProject
	registerIntelliJProjectFunc      = registerIntelliJProject
	ideRunTokenFunc                  = newIDEOpenToken
)

type (
	VSCodeLauncher   func(core.Context, config.OpenResult) error
	IntelliJLauncher func(core.Context, config.OpenResult, prompt.PromptRunner) error
)

func LaunchVSCode(ctx core.Context, result config.OpenResult) error {
	return launchIDEURI(ctx, result, vscodeRemoteFolderURI(result))
}

func launchIDEURI(ctx core.Context, result config.OpenResult, uri string) error {
	if err := ensureLocalSSHDKnownHostFunc(ctx, result); err != nil {
		return err
	}
	command, args, err := ideLaunchCommand(desktop.CurrentHostOS(), uri)
	if err != nil {
		return err
	}
	ctx.TraceCommand("", command, args...)
	if ctx.DryRun {
		return nil
	}
	technical, err := ideOpenURIFunc(ctx, command, args)
	if err != nil {
		return formatIDEOpenError("IDE", desktop.CurrentHostOS(), result, technical, err, "")
	}
	return nil
}

func vscodeRemoteFolderURI(result config.OpenResult) string {
	info := sshconn.SSHConnectionInfoForResult(result)
	return "vscode://vscode-remote/ssh-remote+" + url.PathEscape(info.HostAlias) + (&url.URL{Path: info.WorkspacePath}).EscapedPath()
}

func ideLaunchCommand(hostOS model.HostOS, uri string) (string, []string, error) {
	switch hostOS {
	case model.HostOSDarwin:
		return "open", []string{uri}, nil
	case model.HostOSLinux:
		return "xdg-open", []string{uri}, nil
	case model.HostOSWindows:
		return "cmd", []string{"/c", "start", "", uri}, nil
	case model.HostOSUnknown:
		fallthrough
	default:
		return "", nil, fmt.Errorf("opening IDE links is unsupported on %s", hostOS)
	}
}

func openIDEURICommand(ctx core.Context, command string, args []string) (string, error) {
	cmd := ideExecCommand(command, args...)
	core.HideConsoleWindow(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = ctx.Stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	technical := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(stderr.String()),
		strings.TrimSpace(stdout.String()),
	}, "\n"))
	if technical == "" && err != nil {
		technical = err.Error()
	}
	return technical, err
}

func startIDEURICommand(ctx core.Context, command string, args []string) (string, error) {
	cmd := ideExecCommand(command, args...)
	core.HideConsoleWindow(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdin = nil
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Start()
	technical := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(stderr.String()),
		strings.TrimSpace(stdout.String()),
	}, "\n"))
	if technical == "" && err != nil {
		technical = err.Error()
	}
	if err != nil {
		return technical, err
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return "", nil
}

func formatIDEOpenError(ideName string, hostOS model.HostOS, result config.OpenResult, technical string, err error, extra string) error {
	parts := []string{
		fmt.Sprintf("%s launcher failed on %s.", ideName, hostOSLabel(hostOS)),
		fmt.Sprintf("Fallback: connect manually in JetBrains over SSH to host %q and open %q.", sshconn.SSHConnectionInfoForResult(result).HostAlias, sshconn.SSHConnectionInfoForResult(result).WorkspacePath),
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		parts = append(parts, extra)
	}
	if technical = strings.TrimSpace(technical); technical != "" {
		parts = append(parts, "Technical error: "+technical)
	} else if err != nil {
		parts = append(parts, "Technical error: "+err.Error())
	}
	return errors.New(strings.Join(parts, "\n"))
}

func hostOSLabel(hostOS model.HostOS) string {
	switch hostOS {
	case model.HostOSDarwin:
		return "macOS"
	case model.HostOSLinux:
		return "Linux"
	case model.HostOSWindows:
		return "Windows"
	case model.HostOSUnknown:
		fallthrough
	default:
		return string(hostOS)
	}
}

func newIDEOpenToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

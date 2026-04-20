package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
)

var (
	ideExecCommand                   = exec.Command
	ideLookPath                      = exec.LookPath
	ideSleep                         = time.Sleep
	ideUserHomeDir                   = os.UserHomeDir
	ideGlob                          = filepath.Glob
	ideStat                          = os.Stat
	ideOpenURIFunc                   = openIDEURICommand
	runJetBrainsBootstrapAttemptFunc = runJetBrainsBootstrapAttempt
	openInstalledIntelliJAppFunc     = openInstalledIntelliJApp
	registerIntelliJProjectFunc      = registerIntelliJProject
)

type (
	VSCodeLauncher   func(common.Context, common.OpenResult) error
	IntelliJLauncher func(common.Context, common.OpenResult, PromptRunner) error
)

func launchVSCode(ctx common.Context, result common.OpenResult) error {
	return launchIDEURI(ctx, result, vscodeRemoteFolderURI(result))
}

func launchIntelliJ(ctx common.Context, result common.OpenResult, _ PromptRunner) error {
	if err := ensureLocalSSHDKnownHostFunc(ctx, result); err != nil {
		return err
	}
	if err := registerIntelliJProjectFunc(ctx, result, currentHostOS()); err != nil {
		return err
	}
	if openErr := openInstalledIntelliJAppFunc(ctx, result, currentHostOS()); openErr != nil {
		return formatIDEOpenError("IntelliJ IDEA", currentHostOS(), result, "", openErr, "")
	}
	emitIntelliJManualOpenGuidance(ctx, result)
	return nil
}

func launchIDEURI(ctx common.Context, result common.OpenResult, uri string) error {
	if err := ensureLocalSSHDKnownHostFunc(ctx, result); err != nil {
		return err
	}
	command, args, err := ideLaunchCommand(currentHostOS(), uri)
	if err != nil {
		return err
	}
	ctx.TraceCommand("", command, args...)
	if ctx.DryRun {
		return nil
	}
	technical, err := ideOpenURIFunc(ctx, command, args)
	if err != nil {
		return formatIDEOpenError("IDE", currentHostOS(), result, technical, err, "")
	}
	return nil
}

func vscodeRemoteFolderURI(result common.OpenResult) string {
	info := common.SSHConnectionInfoForResult(result)
	return "vscode://vscode-remote/ssh-remote+" + url.PathEscape(info.HostAlias) + (&url.URL{Path: info.WorkspacePath}).EscapedPath()
}

func ideLaunchCommand(hostOS common.HostOS, uri string) (string, []string, error) {
	switch hostOS {
	case common.HostOSDarwin:
		return "open", []string{uri}, nil
	case common.HostOSLinux:
		return "xdg-open", []string{uri}, nil
	case common.HostOSWindows:
		return "cmd", []string{"/c", "start", "", uri}, nil
	default:
		return "", nil, fmt.Errorf("opening IDE links is unsupported on %s", hostOS)
	}
}

func openIDEURICommand(ctx common.Context, command string, args []string) (string, error) {
	cmd := ideExecCommand(command, args...)
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

func formatIDEOpenError(ideName string, hostOS common.HostOS, result common.OpenResult, technical string, err error, extra string) error {
	parts := []string{
		fmt.Sprintf("%s launcher failed on %s.", ideName, hostOSLabel(hostOS)),
		fmt.Sprintf("Fallback: connect manually in JetBrains over SSH to host %q and open %q.", common.SSHConnectionInfoForResult(result).HostAlias, common.SSHConnectionInfoForResult(result).WorkspacePath),
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

func hostOSLabel(hostOS common.HostOS) string {
	switch hostOS {
	case common.HostOSDarwin:
		return "macOS"
	case common.HostOSLinux:
		return "Linux"
	case common.HostOSWindows:
		return "Windows"
	default:
		return string(hostOS)
	}
}

type jetbrainsBootstrapAttempt struct {
	command string
	args    []string
	runSync bool
}

func runJetBrainsBootstrapAttempt(ctx common.Context, attempt jetbrainsBootstrapAttempt) error {
	ctx.TraceCommand("", attempt.command, attempt.args...)
	if ctx.DryRun {
		return nil
	}
	cmd := ideExecCommand(attempt.command, attempt.args...)
	cmd.Stdin = nil
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	if attempt.runSync {
		if err := cmd.Run(); err != nil {
			return err
		}
	} else {
		if err := cmd.Start(); err != nil {
			return err
		}
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	}
	ideSleep(2 * time.Second)
	return nil
}

func openInstalledIntelliJApp(ctx common.Context, _ common.OpenResult, hostOS common.HostOS) error {
	attempt, ok := resolveInstalledIntelliJAttempt(hostOS)
	if !ok {
		return fmt.Errorf("IntelliJ IDEA is not available on this host")
	}
	return runJetBrainsBootstrapAttemptFunc(ctx, attempt)
}

func registerIntelliJProject(ctx common.Context, result common.OpenResult, hostOS common.HostOS) error {
	optionsDir, err := resolveIntelliJOptionsDir(hostOS)
	if err != nil {
		return nil
	}
	info := common.SSHConnectionInfoForResult(result)
	entry := jetbrainsconfig.ProjectEntry{
		ConfigID:       jetbrainsconfig.StableConfigID(info.HostAlias),
		HostAlias:      info.HostAlias,
		User:           info.User,
		IdentityFile:   info.PrivateKeyPath,
		ProjectPath:    info.WorkspacePath,
		Port:           info.Port,
		ProductCode:    "IU",
		TimestampMilli: time.Now().UnixMilli(),
	}
	ctx.Trace(fmt.Sprintf("write IntelliJ SSH project config in %s", optionsDir))
	if ctx.DryRun {
		return nil
	}
	return jetbrainsconfig.UpsertOptionsFiles(optionsDir, entry)
}

func resolveInstalledIntelliJAttempt(hostOS common.HostOS) (jetbrainsBootstrapAttempt, bool) {
	switch hostOS {
	case common.HostOSDarwin:
		return jetbrainsBootstrapAttempt{
			command: "open",
			args:    []string{"-a", "IntelliJ IDEA"},
			runSync: true,
		}, true
	case common.HostOSLinux:
		attempts := lookPathBootstrapAttempts([]string{"idea", "idea64", "intellij-idea-ultimate"})
		if len(attempts) > 0 {
			return attempts[0], true
		}
	case common.HostOSWindows:
		attempts := lookPathBootstrapAttempts([]string{"idea64.exe", "idea.exe"})
		if len(attempts) > 0 {
			return attempts[0], true
		}
	}
	return jetbrainsBootstrapAttempt{}, false
}

func emitIntelliJManualOpenGuidance(ctx common.Context, result common.OpenResult) {
	out := ctx.Stderr
	if out == nil {
		out = ctx.Stdout
	}
	if out == nil {
		return
	}
	info := common.SSHConnectionInfoForResult(result)
	_, _ = fmt.Fprintf(out,
		"Opened IntelliJ IDEA locally.\n"+
			"Complete the remote connection in IntelliJ:\n"+
			"  Remote Development -> SSH\n"+
			"  host: %s\n"+
			"  user: %s\n"+
			"  key: %s\n"+
			"  project: %s\n",
		info.HostAlias,
		info.User,
		valueOrNone(info.PrivateKeyPath),
		info.WorkspacePath,
	)
}

func lookPathBootstrapAttempts(candidates []string) []jetbrainsBootstrapAttempt {
	attempts := make([]jetbrainsBootstrapAttempt, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		path, err := ideLookPath(candidate)
		if err != nil {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		attempts = append(attempts, jetbrainsBootstrapAttempt{
			command: path,
			runSync: false,
		})
	}
	return attempts
}

func resolveIntelliJOptionsDir(hostOS common.HostOS) (string, error) {
	homeDir, err := ideUserHomeDir()
	if err != nil {
		return "", err
	}

	var baseDir string
	switch hostOS {
	case common.HostOSDarwin:
		baseDir = filepath.Join(homeDir, "Library", "Application Support", "JetBrains")
	case common.HostOSLinux:
		baseDir = filepath.Join(homeDir, ".config", "JetBrains")
	case common.HostOSWindows:
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			baseDir = filepath.Join(appData, "JetBrains")
		}
	default:
		return "", fmt.Errorf("unsupported host OS: %s", hostOS)
	}
	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("JetBrains options directory base is not configured")
	}

	matches, err := ideGlob(filepath.Join(baseDir, "IntelliJIdea*", "options"))
	if err != nil {
		return "", err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(matches))
	for _, match := range matches {
		info, err := ideStat(match)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{path: match, modTime: info.ModTime()})
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("IntelliJ IDEA options directory not found under %s", baseDir)
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.modTime.Equal(b.modTime) {
			return strings.Compare(b.path, a.path)
		}
		if a.modTime.After(b.modTime) {
			return -1
		}
		return 1
	})
	return candidates[0].path, nil
}

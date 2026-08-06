package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// ignoreAlreadyClosed makes a session's Close idempotent. A PTY whose process
// already exited has had its file closed by the streaming goroutine, so a later
// teardown gets "file already closed" — which is the desired end state, not a
// failure. Reporting it as one made "close environment" fail for an env whose
// session happened to die first, and the desktop then left the row rendered as
// open because the caller aborts its unwind on error.
func ignoreAlreadyClosed(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

type terminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
	// Pid returns 0 when no real process backs the session; the stale-shell
	// detector uses it to decide whether a session whose Wait hasn't returned
	// is still alive.
	Pid() int
	// Alive reports whether the backing process is still running. It must not
	// depend on OpenProcess/os.FindProcess: on locked-down Windows an endpoint
	// security agent denies OpenProcess, which would make a live shell look dead
	// and surface a false "shell exited unexpectedly". Implementations use the
	// process handle they already hold (Windows) or a signal-0 probe (POSIX).
	Alive() bool
}

type startTerminalSessionParams struct {
	Dir          string
	Executable   string
	Args         []string
	Env          []string
	Cols         int
	Rows         int
	InitialInput []byte
}

func resolveCLIExecutable() string {
	// ERUN_APP_CLI is a test seam: the Playwright harness points it at an inert
	// `erun` stub so the ERun/AI tabs never resolve a real build artifact sitting
	// next to the app binary and drive `erun open` against the stubbed cluster,
	// which would loop the env-open specs red.
	if override := strings.TrimSpace(os.Getenv("ERUN_APP_CLI")); override != "" {
		return override
	}
	executableName := "erun"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}

	executable, err := os.Executable()
	if err == nil {
		if resolved := resolveCLIExecutableFromPath(runtime.GOOS, executable, executableName); resolved != "" {
			return resolved
		}
	}

	if path, err := exec.LookPath(executableName); err == nil {
		return path
	}
	return executableName
}

func resolveCLIExecutableFromPath(goos, appExecutable, executableName string) string {
	executableDir := filepath.Dir(appExecutable)
	candidates := []string{
		filepath.Join(executableDir, executableName),
	}

	if goos == "darwin" && filepath.Base(executableDir) == "MacOS" {
		candidates = append(candidates, filepath.Clean(filepath.Join(executableDir, "..", "..", "..", executableName)))
	}

	candidates = append(candidates, filepath.Clean(filepath.Join(executableDir, "..", "..", "erun-cli", "bin", executableName)))

	for _, candidate := range candidates {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func runIDECommand(ctx context.Context, params startTerminalSessionParams) (string, error) {
	cmd := exec.CommandContext(ctx, params.Executable, params.Args...)
	eruncommon.HideConsoleWindow(cmd)
	cmd.Dir = resolveTerminalStartDir(params.Dir)
	if len(params.Env) > 0 {
		cmd.Env = append(os.Environ(), params.Env...)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// launchHostArtifactDetached starts a host binary and detaches it so it keeps
// running independently of the desktop; HideConsoleWindow suppresses the console
// flash a windowless desktop child would otherwise get on Windows. It backs the
// host "run artifact" facade so a binary an agent cross-built in the pod can be
// run or debugged on the host.
func launchHostArtifactDetached(exePath, dir string) error {
	cmd := exec.Command(exePath)
	cmd.Dir = dir
	eruncommon.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %s: %w", filepath.Base(exePath), err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

func buildOpenCommand(cliPath, tenant, environment string) string {
	if runtime.GOOS == "windows" {
		return "& " + powerShellQuote(cliPath) + " open " + powerShellQuote(strings.TrimSpace(tenant)) + " " + powerShellQuote(strings.TrimSpace(environment))
	}
	return shellQuote(cliPath) + " open " + shellQuote(strings.TrimSpace(tenant)) + " " + shellQuote(strings.TrimSpace(environment))
}

func buildOpenArgs(tenant, environment string) []string {
	return []string{"open", strings.TrimSpace(tenant), strings.TrimSpace(environment)}
}

// withAppSession makes the `erun open` a reattachable dtach session so closing
// or reopening a tab (or a transient kubectl-exec drop) reconnects to the running
// shell instead of spawning a parallel one, and the AI tab's work keeps running
// in the pod meanwhile.
func withAppSession(args []string, sessionID string, ai, contribute bool) []string {
	args = append(args, "--app-session", sessionID)
	if contribute {
		args = append(args, "--contribute")
	}
	if ai {
		args = append(args, "--ai")
	}
	return args
}

func buildOpenIDEArgs(selection uiSelection, ide string) []string {
	args := buildOpenArgs(selection.Tenant, selection.Environment)
	switch strings.TrimSpace(ide) {
	case "vscode":
		return append(args, "--vscode")
	case "intellij":
		return append(args, "--intellij")
	default:
		return args
	}
}

func buildLocalOpenIDEParams(result eruncommon.OpenResult, ide string) (startTerminalSessionParams, error) {
	projectPath := strings.TrimSpace(result.RepoPath)
	if projectPath == "" {
		return startTerminalSessionParams{}, fmt.Errorf("local project path is required")
	}
	executable, args, err := localOpenIDECommand(runtime.GOOS, strings.TrimSpace(ide), projectPath)
	if err != nil {
		return startTerminalSessionParams{}, err
	}
	return startTerminalSessionParams{
		Dir:        projectPath,
		Executable: executable,
		Args:       args,
	}, nil
}

func localOpenIDECommand(goos, ide, projectPath string) (string, []string, error) {
	switch strings.TrimSpace(goos) {
	case "darwin":
		appName, err := localOpenIDEAppName(ide)
		if err != nil {
			return "", nil, err
		}
		return "open", []string{"-a", appName, projectPath}, nil
	case "linux":
		command, err := localOpenIDEExecutable(ide)
		if err != nil {
			return "", nil, err
		}
		return command, []string{projectPath}, nil
	case "windows":
		command, err := localOpenIDEWindowsCommand(ide)
		if err != nil {
			return "", nil, err
		}
		return "cmd", []string{"/c", "start", "", command, projectPath}, nil
	default:
		return "", nil, fmt.Errorf("opening local IDE projects is unsupported on %s", goos)
	}
}

func localOpenIDEAppName(ide string) (string, error) {
	switch strings.TrimSpace(ide) {
	case "vscode":
		return "Visual Studio Code", nil
	case "intellij":
		return "IntelliJ IDEA", nil
	default:
		return "", fmt.Errorf("unsupported IDE %q", ide)
	}
}

func localOpenIDEExecutable(ide string) (string, error) {
	switch strings.TrimSpace(ide) {
	case "vscode":
		return "code", nil
	case "intellij":
		return "idea", nil
	default:
		return "", fmt.Errorf("unsupported IDE %q", ide)
	}
}

func localOpenIDEWindowsCommand(ide string) (string, error) {
	switch strings.TrimSpace(ide) {
	case "vscode":
		return "code", nil
	case "intellij":
		return "idea64", nil
	default:
		return "", fmt.Errorf("unsupported IDE %q", ide)
	}
}

func buildSSHDInitArgs(selection uiSelection) []string {
	return []string{"sshd", "init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
}

func buildDoctorArgs(selection uiSelection) []string {
	return []string{"doctor", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
}

func buildOpenNoShellArgs(tenant, environment string) []string {
	return []string{"open", strings.TrimSpace(tenant), strings.TrimSpace(environment), "--no-shell", "--no-alias-prompt"}
}

func ensureMCPViaOpenCommand(ctx context.Context, cliPath string, result eruncommon.OpenResult) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	eruncommon.HideConsoleWindow(cmd)
	eruncommon.BoundCommandWait(cmd)
	cmd.Env = append(os.Environ(), "ERUN_IDLE_PROBE=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("activate MCP port-forward: %w", err)
	}
	return fmt.Errorf("activate MCP port-forward: %w: %s", err, detail)
}

// runOpenForReconnect streams the `erun open` child's output through onLine so
// the desktop shows progress while a possibly-slow open (which may trigger a
// runtime deploy) is in flight, and folds the trailing stderr into the returned
// error so the user still sees the actionable detail on failure.
func runOpenForReconnect(ctx context.Context, cliPath string, result eruncommon.OpenResult, onLine func(string)) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	eruncommon.HideConsoleWindow(cmd)
	eruncommon.BoundCommandWait(cmd)
	cmd.Env = append(os.Environ(), "ERUN_IDLE_PROBE=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("activate MCP port-forward: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("activate MCP port-forward: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("activate MCP port-forward: %w", err)
	}
	var lastErr strings.Builder
	scan := func(reader io.Reader, captureErr bool) {
		scanReconnectOutput(reader, captureErr, onLine, &lastErr)
	}
	done := make(chan struct{}, 2)
	go func() { scan(stdout, false); done <- struct{}{} }()
	go func() { scan(stderr, true); done <- struct{}{} }()
	<-done
	<-done
	if err := cmd.Wait(); err != nil {
		detail := strings.TrimSpace(lastErr.String())
		if detail == "" {
			return fmt.Errorf("activate MCP port-forward: %w", err)
		}
		return fmt.Errorf("activate MCP port-forward: %w: %s", err, detail)
	}
	return nil
}

func scanReconnectOutput(reader io.Reader, captureErr bool, onLine func(string), lastErr *strings.Builder) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if onLine != nil && strings.TrimSpace(line) != "" {
			onLine(line)
		}
		if captureErr {
			if lastErr.Len() > 0 {
				lastErr.WriteByte('\n')
			}
			lastErr.WriteString(line)
		}
	}
}

func ensureSSHDViaOpenCommand(ctx context.Context, cliPath string, result eruncommon.OpenResult) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	eruncommon.HideConsoleWindow(cmd)
	eruncommon.BoundCommandWait(cmd)
	cmd.Env = append(os.Environ(), "ERUN_IDLE_PROBE=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("activate SSHD port-forward: %w", err)
	}
	return fmt.Errorf("activate SSHD port-forward: %w: %s", err, detail)
}

func buildInitArgs(selection uiSelection) []string {
	envType := strings.TrimSpace(selection.Type)
	if envType == "" {
		// Older frontends that don't yet send Type still expect the
		// pre-Type behavior — pinned to remote-agent. The CLI itself
		// defaults to local-agent when --type is unset, which would be
		// a silent behavior change for the desktop flow.
		envType = "remote-agent"
	}
	args := []string{"init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment), "--type=" + envType}
	args = appendInitOptionalFlags(args, selection)
	if envType == "local-agent" {
		if localRepoPath := strings.TrimSpace(selection.LocalRepoPath); localRepoPath != "" {
			args = append(args, "--project-root", localRepoPath)
		}
	}
	args = append(
		args,
		"--set-default-tenant="+boolArg(selection.SetDefaultTenant),
		"--confirm-environment=true",
	)
	if selection.NoGit {
		args = append(args, "--no-git")
	}
	return args
}

func appendInitOptionalFlags(args []string, selection uiSelection) []string {
	pairs := []struct{ flag, value string }{
		{"--version", strings.TrimSpace(selection.Version)},
		{"--runtime-image", strings.TrimSpace(selection.RuntimeImage)},
		{"--runtime-cpu", strings.TrimSpace(selection.RuntimeCPU)},
		{"--runtime-memory", strings.TrimSpace(selection.RuntimeMemory)},
		{"--kubernetes-context", strings.TrimSpace(selection.KubernetesContext)},
	}
	// The cluster registry and a static container registry are mutually exclusive
	// (`erun init` rejects both); cluster wins when selected.
	if !selection.ClusterRegistry {
		pairs = append(pairs, struct{ flag, value string }{"--container-registry", strings.TrimSpace(selection.ContainerRegistry)})
	}
	for _, pair := range pairs {
		if pair.value != "" {
			args = append(args, pair.flag, pair.value)
		}
	}
	if selection.ClusterRegistry {
		args = append(args, "--cluster-registry")
	}
	return args
}

func buildDeployArgs(selection uiSelection) []string {
	args := []string{"deploy", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		// deploy is a pure primitive: it requires an explicit version. With no
		// version the operator means "redeploy what this env already runs", so
		// install the persisted version via --current rather than erroring.
		args = append(args, "--current")
	}
	if image := deployRuntimeImageOverride(selection); image != "" {
		args = append(args, "--runtime-image", image)
	}
	args = appendDeployComponentsFlag(args, selection)
	return args
}

// appendDeployComponentsFlag threads the operator's exact component selection
// into the pure deploy primitive rather than reaching for a convenience switch
// (erun-ui/AGENTS.md); an empty selection appends nothing so deploy falls back to
// the env's saved default.
func appendDeployComponentsFlag(args []string, selection uiSelection) []string {
	names := make([]string, 0, len(selection.Components))
	for _, raw := range selection.Components {
		if name := strings.TrimSpace(raw); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return args
	}
	return append(args, "--components", strings.Join(names, ","))
}

// deployRuntimeImageOverride returns the runtime image to force via
// `deploy --runtime-image`, or "" when the pick is the env's own image and needs
// no override. An override lets an operator bootstrap a new env on the shared
// ERun base image before the tenant's own <tenant>-devops image exists.
func deployRuntimeImageOverride(selection uiSelection) string {
	image := strings.TrimSpace(selection.RuntimeImage)
	if image == "" {
		return ""
	}
	repo := image
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		repo = repo[idx+1:]
	}
	if idx := strings.IndexAny(repo, ":@"); idx >= 0 {
		repo = repo[:idx]
	}
	if repo == eruncommon.RuntimeReleaseName(strings.TrimSpace(selection.Tenant)) {
		return ""
	}
	return image
}

// buildUpgradeArgs scopes `erun upgrade` to one env so the Upgrade-all flow can
// upgrade each member in parallel; a selection Version pins the exact target the
// operator picked from the versions an env's registries offered.
func buildUpgradeArgs(selection uiSelection) []string {
	args := []string{"upgrade", "--tenant", selection.Tenant, "--environment", selection.Environment}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	}
	return args
}

func buildCloudInitAWSArgs() []string {
	return []string{"cloud", "init", "aws"}
}

func buildCloudInitCloudflareArgs() []string {
	return []string{"cloud", "init", "cloudflare"}
}

func resolveTerminalStartDir(preferred string) string {
	candidates := []string{strings.TrimSpace(preferred)}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	candidates = append(candidates, ".")

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}

	return "."
}

func resolveDeployStartDir(findProjectRoot eruncommon.ProjectFinderFunc, result eruncommon.OpenResult) string {
	if findProjectRoot != nil {
		if _, projectRoot, err := findProjectRoot(); err == nil && strings.TrimSpace(projectRoot) != "" {
			return resolveTerminalStartDir(projectRoot)
		}
	}
	if result.RemoteRepo() {
		return resolveTerminalStartDir("")
	}
	return resolveTerminalStartDir(result.RepoPath)
}

const defaultAITool = "claude"

func resolveLocalShellCommand(goos string) (string, []string) {
	if strings.TrimSpace(goos) == "windows" {
		// ConPTY resolves a non-absolute executable relative to the session's
		// start dir (not PATH), so a bare "powershell.exe" becomes
		// "<startDir>\powershell.exe" and fails with "the system cannot find the
		// file specified". Return an absolute shell path. $SHELL is ignored on
		// Windows — it is typically a POSIX path from Git Bash that ConPTY cannot
		// launch. Prefer PowerShell 7 (pwsh), fall back to Windows PowerShell.
		for _, name := range []string{"pwsh.exe", "powershell.exe"} {
			if resolved, err := exec.LookPath(name); err == nil {
				return resolved, []string{"-NoLogo"}
			}
		}
		return "powershell.exe", []string{"-NoLogo"}
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell, nil
	}
	return "/bin/bash", nil
}

// claudeEffortLevels enumerates the selectable Claude effort levels in ascending
// order. The first five mirror `claude --effort`; ultracode sits above max as
// "everything on". Must stay in lockstep with erun-common's claudeEffortLevels
// and the frontend fallback in state.ts.
var claudeEffortLevels = []string{"low", "medium", "high", "xhigh", "max", "ultracode"}

const defaultClaudeEffort = "ultracode"

func claudeEffortLevelOptions() []string {
	out := make([]string, len(claudeEffortLevels))
	copy(out, claudeEffortLevels)
	return out
}

func formatLaunchCommand(params startTerminalSessionParams) string {
	parts := make([]string, 0, len(params.Args)+1)
	parts = append(parts, params.Executable)
	parts = append(parts, params.Args...)
	return strings.Join(parts, " ")
}

func formatLocalCommandLog(command, label string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	suffix := ""
	if label = strings.TrimSpace(label); label != "" {
		suffix = "  \x1b[2;3m(running in " + label + ")\x1b[0m"
	}
	return "\x1b[2m$ " + command + "\x1b[0m" + suffix + "\r\n"
}

func localSessionBanner(selection uiSelection) []byte {
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return nil
	}
	// Emitted as a terminal-output event (not pty input) so it doesn't get
	// fed to the shell. ANSI dim makes it look like an inline comment so it
	// doesn't compete visually with real shell output.
	banner := fmt.Sprintf("\x1b[2m# Local host shell for %s/%s — env shell in ERun tab, %s in AI tab\x1b[0m\r\n", tenant, environment, defaultAITool)
	return []byte(banner)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func boolArg(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

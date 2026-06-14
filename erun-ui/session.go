package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

type terminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
	// Pid returns the OS process id of the underlying shell when one is
	// known, or 0 when the implementation does not back the session
	// with a real process. The stale-shell detector consults this to
	// decide whether a session whose Wait hasn't returned is still
	// alive.
	Pid() int
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
	cmd.Dir = resolveTerminalStartDir(params.Dir)
	if len(params.Env) > 0 {
		cmd.Env = append(os.Environ(), params.Env...)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
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

// withAppSession appends the desktop persistent-session flags to an `erun open`
// argv so the remote shell runs as a reattachable dtach session: closing or
// reopening the tab (or a transient kubectl-exec drop) reconnects to the running
// shell instead of spawning a parallel one, and the AI tab's claude keeps
// working in the pod meanwhile. sessionID is stable per (tab kind, slot) so the
// reattach lands on the same session; the AI/contribute launch now happens
// pod-side (no typed prelude). See issue #478.
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

// runOpenForReconnect runs the same `erun open --no-shell --no-alias-prompt`
// child process as ensureMCPViaOpenCommand, but streams stdout/stderr lines
// via onLine so the desktop UI can show progress while the open (and any
// runtime deploy it triggers) is in flight. The trailing buffered output is
// included verbatim in the returned error so the user still sees the
// actionable detail when the command exits non-zero.
func runOpenForReconnect(ctx context.Context, cliPath string, result eruncommon.OpenResult, onLine func(string)) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
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

func ensureSSHDViaOpenCommand(ctx context.Context, cliPath string, result eruncommon.OpenResult) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
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
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	}
	if runtimeImage := strings.TrimSpace(selection.RuntimeImage); runtimeImage != "" {
		args = append(args, "--runtime-image", runtimeImage)
	}
	if runtimeCPU := strings.TrimSpace(selection.RuntimeCPU); runtimeCPU != "" {
		args = append(args, "--runtime-cpu", runtimeCPU)
	}
	if runtimeMemory := strings.TrimSpace(selection.RuntimeMemory); runtimeMemory != "" {
		args = append(args, "--runtime-memory", runtimeMemory)
	}
	if kubernetesContext := strings.TrimSpace(selection.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--kubernetes-context", kubernetesContext)
	}
	if containerRegistry := strings.TrimSpace(selection.ContainerRegistry); containerRegistry != "" {
		args = append(args, "--container-registry", containerRegistry)
	}
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

func buildDeployArgs(selection uiSelection) []string {
	args := []string{"deploy", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	}
	return args
}

// buildUpgradeArgs builds the per-environment `erun upgrade` invocation:
// scoped to the selection's tenant + environment so each Upgrade-all member
// upgrades in its own env, in parallel with the others (issue #497). A
// selection Version pins the exact target — used when the operator picked one
// of several newer versions an env's registries offered (issue #527).
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
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell, nil
	}
	switch strings.TrimSpace(goos) {
	case "windows":
		return "powershell.exe", []string{"-NoLogo"}
	default:
		return "/bin/bash", nil
	}
}

// claudeEffortLevels enumerates the selectable Claude effort levels, in
// ascending order. The first five mirror `claude --effort` from
// `claude --help`; ultracode sits above max as "everything on" (xhigh effort
// plus standing workflow orchestration, launched via --settings — see
// erun-common/ai_launch.go, which owns the launch command). Must stay in
// lockstep with erun-common's claudeEffortLevels and the frontend fallback in
// state.ts.
var claudeEffortLevels = []string{"low", "medium", "high", "xhigh", "max", "ultracode"}

// defaultClaudeEffort is the effort level the desktop applies to a Claude AI
// tab when the env has no explicit Effort configured, or has an invalid one
// (issues #469/#491). Ultracode is the default: everything on.
const defaultClaudeEffort = "ultracode"

// claudeEffortLevelOptions returns the valid effort levels for transport to the
// frontend selector.
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

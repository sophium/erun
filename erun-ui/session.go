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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
)

// ignoreAlreadyClosed makes Close idempotent: a PTY whose process exited already
// had its file closed by the streaming goroutine.
func ignoreAlreadyClosed(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

// errKillDidNotLand means the process survived the kill, so the teardown must not
// claim the survivor's eventual exit as its own.
var errKillDidNotLand = errors.New("process outlived the kill")

// killProcessTree judges the kill by the liveness probe, not the kill command's
// exit status: taskkill exits non-zero for an already-exited process.
func killProcessTree(pid int, kill func() error, alive func() bool) error {
	if pid <= 0 {
		return nil
	}
	_ = kill()
	if alive() {
		return fmt.Errorf("%w: pid %d", errKillDidNotLand, pid)
	}
	return nil
}

type terminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
	// Pid returns 0 when no real process backs the session.
	Pid() int
	// Alive must not use OpenProcess/os.FindProcess: EDR agents on Windows deny it,
	// which would make a live shell look dead.
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

// NO_COLOR/FORCE_COLOR defeat the PTY's TERM on presence alone; the Claude Code
// markers would make a claude launched here run as a nested child.
var strippedSessionEnvNames = []string{"NO_COLOR", "FORCE_COLOR", "CLAUDECODE", "CLAUDE_PID"}

const strippedSessionEnvPrefix = "CLAUDE_CODE_"

// terminalSessionEnv order is the contract: scrubbed inherited env, then the
// caller's values, then terminal capabilities.
func terminalSessionEnv(inherited, requested []string) []string {
	env := make([]string, 0, len(inherited)+len(requested)+2)
	for _, entry := range inherited {
		if !stripsFromTerminalSession(entry) {
			env = append(env, entry)
		}
	}
	env = append(env, requested...)
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}

func stripsFromTerminalSession(entry string) bool {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return false
	}
	if strings.HasPrefix(name, strippedSessionEnvPrefix) {
		return true
	}
	for _, stripped := range strippedSessionEnvNames {
		if name == stripped {
			return true
		}
	}
	return false
}

func resolveCLIExecutable() string {
	// ERUN_APP_CLI is a test seam for the Playwright harness's inert `erun` stub.
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
	core.HideConsoleWindow(cmd)
	cmd.Dir = resolveTerminalStartDir(params.Dir)
	if len(params.Env) > 0 {
		cmd.Env = append(os.Environ(), params.Env...)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// launchHostArtifactDetached starts a host binary detached from the desktop.
func launchHostArtifactDetached(exePath, dir string) error {
	cmd := exec.Command(exePath)
	cmd.Dir = dir
	core.HideConsoleWindow(cmd)
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

// withAppSession makes `erun open` a reattachable dtach session so reopening a tab reconnects instead of spawning a new shell.
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

func buildLocalOpenIDEParams(result config.OpenResult, ide string) (startTerminalSessionParams, error) {
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

// reconnectSessionParams marks a respawn as machine-initiated: a stop drops every
// session, and the respawn must not restart the runtime the stop reclaimed.
func reconnectSessionParams(params startTerminalSessionParams) startTerminalSessionParams {
	params.Args = append(append([]string(nil), params.Args...), "--reconnect")
	return params
}

func ensureMCPViaOpenCommand(ctx context.Context, cliPath string, result config.OpenResult) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
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

// runOpenForReconnect passes --reconnect so it never deploys or starts a stopped environment.
func runOpenForReconnect(ctx context.Context, cliPath string, result config.OpenResult, onLine func(string)) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
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

func ensureSSHDViaOpenCommand(ctx context.Context, cliPath string, result config.OpenResult) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
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

func resolveDeployStartDir(findProjectRoot projectseam.ProjectFinderFunc, result config.OpenResult) string {
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

// Headless-test seam (playwright/fixtures/seedRoot.ts): a real POSIX shell with a
// pinned prompt and no rc files, for deterministic terminal-content specs.
const (
	localShellDeterministicPromptOverrideEnvVar = "ERUN_LOCAL_SHELL_OVERRIDE"
	localShellDeterministicPrompt               = "erun-test$ "
)

func resolveLocalShellCommand(goos string) (string, []string) {
	if strings.TrimSpace(goos) == "windows" {
		// ConPTY resolves a bare executable against the start dir, not PATH, so return an
		// absolute path. $SHELL is ignored on Windows (often a Git Bash path ConPTY cannot launch).
		for _, name := range []string{"pwsh.exe", "powershell.exe"} {
			if resolved, err := exec.LookPath(name); err == nil {
				return resolved, []string{"-NoLogo"}
			}
		}
		return "powershell.exe", []string{"-NoLogo"}
	}
	if strings.TrimSpace(os.Getenv(localShellDeterministicPromptOverrideEnvVar)) == "1" {
		script := fmt.Sprintf("unset ENV; export PS1=%s; exec /bin/sh -i", shellQuote(localShellDeterministicPrompt))
		return "/bin/sh", []string{"-c", script}
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell, nil
	}
	return "/bin/bash", nil
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
	// Emitted as terminal output, not pty input, so the shell never sees it.
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

package main

import (
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
}

type startTerminalSessionParams struct {
	Dir        string
	Executable string
	Args       []string
	Env        []string
	Cols       int
	Rows       int
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

func buildOpenCommand(cliPath, tenant, environment string) string {
	if runtime.GOOS == "windows" {
		return "& " + powerShellQuote(cliPath) + " open " + powerShellQuote(strings.TrimSpace(tenant)) + " " + powerShellQuote(strings.TrimSpace(environment))
	}
	return shellQuote(cliPath) + " open " + shellQuote(strings.TrimSpace(tenant)) + " " + shellQuote(strings.TrimSpace(environment))
}

func buildOpenArgs(tenant, environment string, debug ...bool) []string {
	return erunArgs(debugEnabled(debug...), "open", strings.TrimSpace(tenant), strings.TrimSpace(environment))
}

func buildOpenNoShellArgs(tenant, environment string) []string {
	return []string{"open", strings.TrimSpace(tenant), strings.TrimSpace(environment), "--no-shell", "--no-alias-prompt"}
}

func ensureMCPViaOpenCommand(ctx context.Context, cliPath string, result eruncommon.OpenResult) error {
	args := buildOpenNoShellArgs(result.Tenant, result.Environment)
	cmd := exec.CommandContext(ctx, cliPath, args...)
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

func buildInitArgs(selection uiSelection) []string {
	args := erunArgs(selection.Debug, "init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment), "--remote")
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	}
	if runtimeImage := strings.TrimSpace(selection.RuntimeImage); runtimeImage != "" {
		args = append(args, "--runtime-image", runtimeImage)
	}
	if kubernetesContext := strings.TrimSpace(selection.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--kubernetes-context", kubernetesContext)
	}
	if containerRegistry := strings.TrimSpace(selection.ContainerRegistry); containerRegistry != "" {
		args = append(args, "--container-registry", containerRegistry)
	}
	args = append(
		args,
		"--set-default-tenant="+boolArg(selection.SetDefaultTenant),
		"--confirm-environment=true",
	)
	if selection.NoGit {
		args = append(args, "--no-git")
	}
	if selection.Bootstrap {
		args = append(args, "--bootstrap")
	}
	return args
}

func buildDeployArgs(selection uiSelection) []string {
	args := erunArgs(selection.Debug, "open", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment), "--no-shell", "--no-alias-prompt")
	version := selection.Version
	runtimeImage := selection.RuntimeImage
	if version = strings.TrimSpace(version); version != "" {
		args = append(args, "--version", version)
	}
	if runtimeImage = strings.TrimSpace(runtimeImage); runtimeImage != "" {
		args = append(args, "--runtime-image", runtimeImage)
	}
	return args
}

func erunArgs(debug bool, args ...string) []string {
	if !debug {
		return args
	}
	result := make([]string, 0, len(args)+1)
	result = append(result, "-vv")
	result = append(result, args...)
	return result
}

func debugEnabled(values ...bool) bool {
	return len(values) > 0 && values[0]
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

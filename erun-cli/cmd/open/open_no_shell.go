package open

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open"
)

type openNoShellDialect string

const (
	openNoShellDialectPOSIX      openNoShellDialect = "posix"
	openNoShellDialectPowerShell openNoShellDialect = "powershell"
)

func emitLocalShellSetupForOpenResult(ctx core.Context, result config.OpenResult, promptRunner shared.PromptRunner, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	dialect := openNoShellDialectForShell(os.Getenv("SHELL"))
	if stdoutIsTerminalForAliasSetup(stdout) {
		if err := maybeConfigureOpenNoShellAlias(ctx, result, promptRunner, os.Getenv("SHELL"), stderr); err != nil {
			return err
		}
	}

	_, err := io.WriteString(stdout, localShellSetupScript(result, dialect))
	return err
}

func stdoutIsTerminalForAliasSetup(stdout io.Writer) bool {
	return shared.WriterIsTerminal(stdout)
}

// nopWriteCloser keeps promptui from closing the underlying stream.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// UnwrapWriter lets echo suppression see the file underneath.
func (w nopWriteCloser) UnwrapWriter() io.Writer { return w.Writer }

func promptStderr() io.WriteCloser { return nopWriteCloser{os.Stderr} }

func maybeConfigureOpenNoShellAlias(ctx core.Context, result config.OpenResult, promptRunner shared.PromptRunner, shellPath string, stderr io.Writer) error {
	dialect := openNoShellDialectForShell(shellPath)
	aliasName := openNoShellAliasName(result)
	startupFile, aliasConfigured := detectOpenNoShellAliasStartupFile(result, shellPath)
	if aliasConfigured {
		return nil
	}
	if startupFile == "" || promptRunner == nil || dialect == openNoShellDialectPowerShell {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}

	// stderr: promptui repaints from a goroutine and would interleave with the stdout setup script.
	ok, err := shared.ConfirmPromptTo(promptRunner, fmt.Sprintf("add %s to %s", aliasName, startupFile), promptStderr())
	if err != nil {
		return err
	}
	if !ok {
		writeOpenNoShellHintLines(stderr, result, shellPath)
		return nil
	}

	ctx.Trace(fmt.Sprintf("open: append %s alias to %s", aliasName, startupFile))
	if ctx.DryRun {
		return nil
	}
	if err := appendOpenNoShellAlias(startupFile, openNoShellAliasCommand(result, shellPath)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "added %s to %s\n", aliasName, startupFile)
	_, _ = fmt.Fprintf(stderr, "open a new shell to use %s\n", aliasName)
	return nil
}

func writeOpenNoShellHintLines(stderr io.Writer, result config.OpenResult, shellPath string) {
	for _, line := range openNoShellHintLines(result, shellPath) {
		_, _ = fmt.Fprintln(stderr, line)
	}
}

func openNoShellHintLines(result config.OpenResult, shellPath string) []string {
	dialect := openNoShellDialectForShell(shellPath)
	return []string{
		openNoShellHintPrefix(dialect),
		openNoShellAliasCommand(result, shellPath),
	}
}

func openNoShellAliasName(result config.OpenResult) string {
	if strings.TrimSpace(result.Title) != "" {
		return strings.TrimSpace(result.Title)
	}
	return strings.TrimSpace(result.Tenant) + "-" + strings.TrimSpace(result.Environment)
}

func openNoShellAliasCommand(result config.OpenResult, shellPath string) string {
	aliasName := openNoShellAliasName(result)
	command := fmt.Sprintf("erun open %s %s --no-shell", result.Tenant, result.Environment)
	dialect := openNoShellDialectForShell(shellPath)
	if dialect == openNoShellDialectPowerShell {
		return "function " + aliasName + " { " + command + " | Invoke-Expression }"
	}
	if filepath.Base(strings.TrimSpace(shellPath)) == "fish" {
		return "alias " + aliasName + " 'eval (" + command + ")'"
	}
	return "alias " + aliasName + `='eval "$(` + command + `)"'`
}

func detectOpenNoShellAliasStartupFile(result config.OpenResult, shellPath string) (string, bool) {
	if openNoShellDialectForShell(shellPath) == openNoShellDialectPowerShell {
		return "", false
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		return "", false
	}

	preferred, candidates := openNoShellStartupFiles(homeDir, shellPath)
	for _, candidate := range candidates {
		configured, err := startupFileHasAlias(candidate, openNoShellAliasName(result))
		if err != nil {
			continue
		}
		if configured {
			return candidate, true
		}
	}
	return preferred, false
}

func openNoShellStartupFiles(homeDir, shellPath string) (string, []string) {
	switch filepath.Base(strings.TrimSpace(shellPath)) {
	case "bash":
		preferred := filepath.Join(homeDir, ".bashrc")
		return preferred, []string{
			preferred,
			filepath.Join(homeDir, ".bash_profile"),
			filepath.Join(homeDir, ".profile"),
		}
	case "fish":
		preferred := filepath.Join(homeDir, ".config", "fish", "config.fish")
		return preferred, []string{preferred}
	default:
		preferred := filepath.Join(homeDir, ".zshrc")
		return preferred, []string{preferred}
	}
}

func startupFileHasAlias(path, aliasName string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "alias "+aliasName+"=") || strings.HasPrefix(trimmed, "alias "+aliasName+" ") {
			return true, nil
		}
	}
	return false, nil
}

func appendOpenNoShellAlias(path, aliasCommand string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), aliasCommand) {
		return nil
	}

	content := string(data)
	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += aliasCommand + "\n"

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func openNoShellDialectForShell(shellPath string) openNoShellDialect {
	return detectOpenNoShellDialect(shared.CurrentHostOS(), shellPath)
}

func detectOpenNoShellDialect(hostOS model.HostOS, shellPath string) openNoShellDialect {
	switch strings.ToLower(filepath.Base(strings.TrimSpace(shellPath))) {
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		return openNoShellDialectPowerShell
	case "bash", "bash.exe", "zsh", "zsh.exe", "sh", "sh.exe", "fish", "fish.exe":
		return openNoShellDialectPOSIX
	}
	if hostOS == model.HostOSWindows {
		return openNoShellDialectPowerShell
	}
	return openNoShellDialectPOSIX
}

func localShellSetupScript(result config.OpenResult, dialect openNoShellDialect) string {
	if dialect == openNoShellDialectPowerShell {
		commands := []string{
			"kubectl config use-context " + powerShellQuote(strings.TrimSpace(result.EnvConfig.KubernetesContext)) + " | Out-Null",
			"kubectl config set-context --current " + powerShellQuote("--namespace="+config.KubernetesNamespaceName(result.Tenant, result.Environment)) + " | Out-Null",
			"Set-Location -LiteralPath " + powerShellQuote(result.RepoPath),
		}
		return strings.Join(commands, "\n") + "\n"
	}
	return open.LocalShellSetupScript(result)
}

func openNoShellHintPrefix(dialect openNoShellDialect) string {
	if dialect == openNoShellDialectPowerShell {
		return "one-liner function:"
	}
	return "one-liner alias:"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

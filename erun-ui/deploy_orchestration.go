package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// runDeployOrchestration composes build/push/deploy primitives (the desktop may not use
// `build --deploy`, per erun-ui/AGENTS.md), threading the minted version so what deploys is what was built.
func (a *App) runDeployOrchestration(ctx context.Context, selection uimodel.Selection, result config.OpenResult, force bool) error {
	cli := a.deps.resolveCLIPath()
	dir := terminal.ResolveStartDir(result.RepoPath)
	onLine := newActivityTraceLineHandler(a, selection, terminal.KindLocal)

	if result.EnvConfig.ResolvedType() == config.EnvironmentTypeLocalAgent {
		return a.runBuildPushDeployOrchestration(ctx, cli, dir, onLine, selection, force)
	}
	return a.runInstallByReferenceDeploy(ctx, cli, dir, onLine, selection, force)
}

func (a *App) runBuildPushDeployOrchestration(ctx context.Context, cli, dir string, onLine func(string), selection uimodel.Selection, force bool) error {
	buildArgs := []string{"build", "--output", "json"}
	if force {
		buildArgs = append(buildArgs, "--no-incremental")
	}
	out, err := runErunCaptured(ctx, cli, dir, onLine, buildArgs...)
	if err != nil {
		return err
	}
	version := parseBuildResultVersion(out)
	if version == "" {
		return fmt.Errorf("build did not report a version to deploy")
	}
	if _, err := runErunCaptured(ctx, cli, dir, onLine, "push", "--version", version); err != nil {
		return err
	}
	deployArgs := []string{"deploy", selection.Tenant, selection.Environment, "--version", version}
	if force {
		deployArgs = append(deployArgs, "--force")
	}
	deployArgs = terminal.AppendDeployComponentsFlag(deployArgs, selection)
	deployArgs = a.appendMCPAuthPublicKeyFlag(deployArgs)
	_, err = runErunCaptured(ctx, cli, dir, onLine, deployArgs...)
	return err
}

func (a *App) runInstallByReferenceDeploy(ctx context.Context, cli, dir string, onLine func(string), selection uimodel.Selection, force bool) error {
	args := []string{"deploy", selection.Tenant, selection.Environment}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		args = append(args, "--current")
	}
	if image := terminal.DeployRuntimeImageOverride(selection); image != "" {
		args = append(args, "--runtime-image", image)
	}
	if force {
		args = append(args, "--force")
	}
	args = terminal.AppendDeployComponentsFlag(args, selection)
	args = a.appendMCPAuthPublicKeyFlag(args)
	_, err := runErunCaptured(ctx, cli, dir, onLine, args...)
	return err
}

// appendMCPAuthPublicKeyFlag requires the desktop-signed MCP bearer; on failure it
// omits the flag, since an unauthenticated env is recoverable and a blocked deploy is not.
func (a *App) appendMCPAuthPublicKeyFlag(args []string) []string {
	if a.identity == nil {
		return args
	}
	path, err := a.identity.ensurePublicKeyPath()
	if err != nil {
		log.Printf("erun-app: resolve MCP auth public key for deploy: %v", err)
		return args
	}
	if path == "" {
		return args
	}
	return append(args, "--mcp-auth-public-key", path)
}

// deployNeedsBuildOrchestration: local-agent envs (and forced deploys) rebuild; runtime
// envs and pinned versions install by reference; remote-agent envs build in-pod; host
// envs have no pod. Checks ResolvedType, since a host env also satisfies BuildsHere() && !RemoteRepo().
func deployNeedsBuildOrchestration(result config.OpenResult, version string, force bool) bool {
	if result.EnvConfig.ResolvedType() != config.EnvironmentTypeLocalAgent {
		return false
	}
	return force || strings.TrimSpace(version) == ""
}

// maybeStartDeployOrchestration returns false for envs that use in-shell `erun deploy` instead.
func (a *App) maybeStartDeployOrchestration(selection uimodel.Selection, force bool) (uimodel.StartSessionResult, bool) {
	selection = uimodel.NormalizeSelection(selection)
	// a.ctx is nil in unit tests; never fall back to the real CLI and config there.
	if a.ctx == nil || selection.Tenant == "" || selection.Environment == "" {
		return uimodel.StartSessionResult{}, false
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !deployNeedsBuildOrchestration(result, selection.Version, force) {
		return uimodel.StartSessionResult{}, false
	}
	go func() {
		ctx := a.context()
		// Detached from any PTY, so surface failures explicitly unless the app is shutting down.
		if err := a.runDeployOrchestration(ctx, selection, result, force); err != nil && ctx.Err() == nil {
			a.surfaceDeployFailure(selection.Tenant, selection.Environment, deployOrchestrationFailureReason(err))
		}
	}()
	return uimodel.StartSessionResult{Selection: selection, Orchestrated: true}, true
}

const deployOrchestrationFailureReasonMaxLen = 300

// deployOrchestrationFailureReason drops erun's "==> ..." markers, which lead
// captured stderr, and keeps the tool output after them.
func deployOrchestrationFailureReason(err error) string {
	if err == nil {
		return ""
	}
	var cmdErr *erunCommandError
	if errors.As(err, &cmdErr) {
		return truncateFailureReasonTail(cmdErr.meaningfulReason(), deployOrchestrationFailureReasonMaxLen)
	}
	return truncateFailureReasonTail(strings.TrimSpace(err.Error()), deployOrchestrationFailureReasonMaxLen)
}

// truncateFailureReasonTail keeps the tail, where the actual error sits.
func truncateFailureReasonTail(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return "..." + msg[len(msg)-maxLen:]
}

// runErunCaptured returns stdout (the `--output json` result) and forwards stderr lines to onLine.
func runErunCaptured(ctx context.Context, cliPath, dir string, onLine func(string), args ...string) (string, error) {
	if strings.TrimSpace(cliPath) == "" {
		cliPath = "erun"
	}
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), appSessionEnvVar+"=1")
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var stdoutBuf, lastErr strings.Builder
	done := make(chan struct{}, 2)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			stdoutBuf.WriteString(scanner.Text())
			stdoutBuf.WriteByte('\n')
		}
		done <- struct{}{}
	}()
	go func() {
		scanErunStderr(stderrPipe, onLine, &lastErr)
		done <- struct{}{}
	}()
	<-done
	<-done

	if err := cmd.Wait(); err != nil {
		return stdoutBuf.String(), &erunCommandError{
			Command: args[0],
			Err:     err,
			Detail:  strings.TrimSpace(lastErr.String()),
		}
	}
	return stdoutBuf.String(), nil
}

// erunCommandError carries the full captured stderr in Detail.
type erunCommandError struct {
	Command string
	Err     error
	Detail  string
}

func (e *erunCommandError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("erun %s: %v", e.Command, e.Err)
	}
	return fmt.Sprintf("erun %s: %v: %s", e.Command, e.Err, e.Detail)
}

func (e *erunCommandError) Unwrap() error { return e.Err }

// meaningfulReason joins Detail without "==> ..." markers, falling back to the exec error.
func (e *erunCommandError) meaningfulReason() string {
	var kept []string
	for _, line := range strings.Split(e.Detail, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "==> ") {
			continue
		}
		kept = append(kept, trimmed)
	}
	if len(kept) == 0 {
		return fmt.Sprintf("erun %s: %v", e.Command, e.Err)
	}
	return fmt.Sprintf("erun %s: %v: %s", e.Command, e.Err, strings.Join(kept, " "))
}

func scanErunStderr(stderrPipe io.Reader, onLine func(string), lastErr *strings.Builder) {
	scanner := bufio.NewScanner(stderrPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if onLine != nil && strings.TrimSpace(line) != "" {
			onLine(line)
		}
		if lastErr.Len() > 0 {
			lastErr.WriteByte('\n')
		}
		lastErr.WriteString(line)
	}
}

// parseBuildResultVersion scans for the JSON object, tolerating stray lines.
func parseBuildResultVersion(stdout string) string {
	s := strings.TrimSpace(stdout)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	var result model.BuildResult
	if err := json.Unmarshal([]byte(s[start:end+1]), &result); err != nil {
		return ""
	}
	return strings.TrimSpace(result.Version)
}

package activity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// RunDeployOrchestration composes build/push/deploy primitives (the desktop may not use
// `build --deploy`, per erun-ui/AGENTS.md), threading the minted version so what deploys is what was built.
func (s *Service) RunDeployOrchestration(ctx context.Context, selection uimodel.Selection, result config.OpenResult, force bool) error {
	cli := s.d.ResolveCLIPath()
	dir := s.d.StartDir(result.RepoPath)
	onLine := s.TraceLineHandler(selection, KindLocal)

	if result.EnvConfig.ResolvedType() == config.EnvironmentTypeLocalAgent {
		return s.runBuildPushDeployOrchestration(ctx, cli, dir, onLine, selection, force)
	}
	return s.runInstallByReferenceDeploy(ctx, cli, dir, onLine, selection, force)
}

func (s *Service) runBuildPushDeployOrchestration(ctx context.Context, cli, dir string, onLine func(string), selection uimodel.Selection, force bool) error {
	buildArgs := []string{"build", "--output", "json"}
	if force {
		buildArgs = append(buildArgs, "--no-incremental")
	}
	out, err := s.runErunCaptured(ctx, cli, dir, onLine, buildArgs...)
	if err != nil {
		return err
	}
	version := parseBuildResultVersion(out)
	if version == "" {
		return fmt.Errorf("build did not report a version to deploy")
	}
	if _, err := s.runErunCaptured(ctx, cli, dir, onLine, "push", "--version", version); err != nil {
		return err
	}
	deployArgs := []string{"deploy", selection.Tenant, selection.Environment, "--version", version}
	if force {
		deployArgs = append(deployArgs, "--force")
	}
	deployArgs = s.d.AppendDeployComponents(deployArgs, selection)
	deployArgs = s.d.AppendMCPAuthPublicKeyFlag(deployArgs)
	_, err = s.runErunCaptured(ctx, cli, dir, onLine, deployArgs...)
	return err
}

func (s *Service) runInstallByReferenceDeploy(ctx context.Context, cli, dir string, onLine func(string), selection uimodel.Selection, force bool) error {
	args := []string{"deploy", selection.Tenant, selection.Environment}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		args = append(args, "--current")
	}
	if image := s.d.RuntimeImageOverride(selection); image != "" {
		args = append(args, "--runtime-image", image)
	}
	if force {
		args = append(args, "--force")
	}
	args = s.d.AppendDeployComponents(args, selection)
	args = s.d.AppendMCPAuthPublicKeyFlag(args)
	_, err := s.runErunCaptured(ctx, cli, dir, onLine, args...)
	return err
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

// MaybeStartDeployOrchestration returns false for envs that use in-shell `erun deploy` instead.
func (s *Service) MaybeStartDeployOrchestration(selection uimodel.Selection, force bool) (uimodel.StartSessionResult, bool) {
	selection = uimodel.NormalizeSelection(selection)
	// Startup has not run in unit tests; never fall back to the real CLI and config there.
	if !s.d.Started() || selection.Tenant == "" || selection.Environment == "" {
		return uimodel.StartSessionResult{}, false
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !deployNeedsBuildOrchestration(result, selection.Version, force) {
		return uimodel.StartSessionResult{}, false
	}
	go func() {
		ctx := s.d.Context()
		// Detached from any PTY, so surface failures explicitly unless the app is shutting down.
		if err := s.RunDeployOrchestration(ctx, selection, result, force); err != nil && ctx.Err() == nil {
			s.surfaceDeployFailure(selection.Tenant, selection.Environment, deployOrchestrationFailureReason(err))
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
func (s *Service) runErunCaptured(ctx context.Context, cliPath, dir string, onLine func(string), args ...string) (string, error) {
	if strings.TrimSpace(cliPath) == "" {
		cliPath = "erun"
	}
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), s.d.AppSessionEnv)
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

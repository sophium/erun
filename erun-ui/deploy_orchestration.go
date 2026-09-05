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

	eruncommon "github.com/sophium/erun/erun-common"
)

// runDeployOrchestration is the desktop's deploy. The desktop is barred from the
// `build --deploy` operator-convenience switches (erun-ui/AGENTS.md), so it
// composes the pure primitives itself and threads the version `build` minted
// forward, guaranteeing what deploys is exactly what was built.
func (a *App) runDeployOrchestration(ctx context.Context, selection uiSelection, result eruncommon.OpenResult, force bool) error {
	cli := a.deps.resolveCLIPath()
	dir := resolveTerminalStartDir(result.RepoPath)
	onLine := newActivityTraceLineHandler(a, selection, sessionKindLocal)

	if result.EnvConfig.ResolvedType() == eruncommon.EnvironmentTypeLocalAgent {
		return a.runBuildPushDeployOrchestration(ctx, cli, dir, onLine, selection, force)
	}
	return a.runInstallByReferenceDeploy(ctx, cli, dir, onLine, selection, force)
}

func (a *App) runBuildPushDeployOrchestration(ctx context.Context, cli, dir string, onLine func(string), selection uiSelection, force bool) error {
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
	deployArgs = appendDeployComponentsFlag(deployArgs, selection)
	deployArgs = a.appendMCPAuthPublicKeyFlag(deployArgs)
	_, err = runErunCaptured(ctx, cli, dir, onLine, deployArgs...)
	return err
}

func (a *App) runInstallByReferenceDeploy(ctx context.Context, cli, dir string, onLine func(string), selection uiSelection, force bool) error {
	args := []string{"deploy", selection.Tenant, selection.Environment}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		args = append(args, "--current")
	}
	if image := deployRuntimeImageOverride(selection); image != "" {
		args = append(args, "--runtime-image", image)
	}
	if force {
		args = append(args, "--force")
	}
	args = appendDeployComponentsFlag(args, selection)
	args = a.appendMCPAuthPublicKeyFlag(args)
	_, err := runErunCaptured(ctx, cli, dir, onLine, args...)
	return err
}

// appendMCPAuthPublicKeyFlag makes the deployed env require the same
// desktop-signed bearer the desktop sends to its MCP edge. A missing identity
// (as in unit tests) or a key-generation failure proceeds without the flag
// rather than blocking the deploy: an unauthenticated env is recoverable, a
// blocked deploy is not.
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

// deployNeedsBuildOrchestration encodes the desktop's per-env-type deploy policy
// (root AGENTS.md § "Command primitives vs orchestration"): a local-agent env
// rebuilds from scratch, while a runtime env or a pinned published version
// installs by reference. A forced deploy is the rebuild-&-redeploy recovery, so
// it always rebuilds; remote-agent envs build in their own pod, not through
// this local orchestration; a host env has no pod to deploy to at all, so it
// never needs build orchestration either — deploy refuses it outright (see
// EnvConfig.ResolvedType/HasPod in erun-common). Checked against ResolvedType
// rather than the broader "BuildsHere() && !RemoteRepo()" combination, which a
// host env also satisfies.
func deployNeedsBuildOrchestration(result eruncommon.OpenResult, version string, force bool) bool {
	if result.EnvConfig.ResolvedType() != eruncommon.EnvironmentTypeLocalAgent {
		return false
	}
	return force || strings.TrimSpace(version) == ""
}

// maybeStartDeployOrchestration launches the desktop's pure-primitive deploy in
// the background and returns true once started. Runtime/remote envs return false
// so the caller falls back to the in-shell `erun deploy` path.
func (a *App) maybeStartDeployOrchestration(selection uiSelection, force bool) (startSessionResult, bool) {
	selection = normalizeSelection(selection)
	// a.ctx is nil in unit tests; never fall back to the machine's real CLI
	// and config there (the hazard class).
	if a.ctx == nil || selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, false
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil || !deployNeedsBuildOrchestration(result, selection.Version, force) {
		return startSessionResult{}, false
	}
	go func() {
		ctx := a.activityWatcherCtx()
		// The orchestration runs detached from any PTY, so a build/push failure —
		// or a deploy that errors before emitting its "==> Deploy failed" trace —
		// would otherwise vanish: no pods, no cause, and (for a builds-here create)
		// an empty Local shell. That was the erun/local create-with-no-pods report.
		// Surface it as a failed env-status + an actionable notification, unless the
		// app is shutting down (ctx cancelled), which is not a deploy failure.
		if err := a.runDeployOrchestration(ctx, selection, result, force); err != nil && ctx.Err() == nil {
			a.surfaceDeployFailure(selection.Tenant, selection.Environment, deployOrchestrationFailureReason(err))
		}
	}()
	return startSessionResult{Selection: selection, Orchestrated: true}, true
}

// deployOrchestrationFailureReasonMaxLen caps the single-line reason surfaced
// in the failure notification; the full captured output is still available in
// the activity card's Detail (see recordOutputLine).
const deployOrchestrationFailureReasonMaxLen = 300

// deployOrchestrationFailureReason condenses a background build/push/deploy error
// into a single-line notification reason. For an *erunCommandError it drops
// erun's own "==> ..." progress markers — captured stderr's first line is
// always one, since erun routes Info to stderr, and it carries no diagnostic
// value — and keeps the actual compiler/tool output that follows, which used
// to be discarded by truncating to that first line.
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

// truncateFailureReasonTail keeps the tail of an over-long reason rather than
// the head: the actionable content (the actual error) sits at the end of
// captured tool output, after any preceding log noise.
func truncateFailureReasonTail(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return "..." + msg[len(msg)-maxLen:]
}

// runErunCaptured runs `erun <args>` in dir and returns its captured stdout. erun
// writes its `--output json` result to stdout and trace/`==>` lines to stderr
// (forwarded to onLine), so the structured result stays clean of progress noise.
func runErunCaptured(ctx context.Context, cliPath, dir string, onLine func(string), args ...string) (string, error) {
	if strings.TrimSpace(cliPath) == "" {
		cliPath = "erun"
	}
	cmd := exec.CommandContext(ctx, cliPath, args...)
	eruncommon.HideConsoleWindow(cmd)
	eruncommon.BoundCommandWait(cmd)
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

// erunCommandError reports a failed `erun <Command>` subprocess invocation
// alongside its full captured stderr, so a caller can pick the meaningful
// content out of Detail (see meaningfulReason) instead of string-parsing an
// already-flattened error message.
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

// meaningfulReason drops erun's own "==> ..." progress markers from Detail —
// each one is routed to stderr via Info — and joins what remains, which is
// the actual compiler/tool output a build/push/deploy failure produced. Falls
// back to the bare exec error when Detail carried nothing but markers (e.g. a
// process that exited non-zero before producing any tool output).
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

// parseBuildResultVersion extracts the minted version from `erun build
// --output json` stdout. It scans for the JSON object so a stray prefix/suffix
// line cannot break the parse.
func parseBuildResultVersion(stdout string) string {
	s := strings.TrimSpace(stdout)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return ""
	}
	var result eruncommon.BuildResult
	if err := json.Unmarshal([]byte(s[start:end+1]), &result); err != nil {
		return ""
	}
	return strings.TrimSpace(result.Version)
}

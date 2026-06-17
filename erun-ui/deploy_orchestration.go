package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// runDeployOrchestration composes the pure command primitives for an Operator's
// deploy the way the desktop is required to (erun-ui/AGENTS.md): it never uses
// the `build --deploy` / `deploy --build` operator-convenience switches. For a
// builds-here agent env with the source on this machine it runs build -> push
// -> deploy as discrete captured subprocesses, capturing the version `build`
// minted from `--output json` and threading it into push and deploy so what
// deploys is exactly what was built. For a runtime env (or a remote-repo env
// that consumes a published chart) it installs the selected version by
// reference, or --current to redeploy the version the env already runs.
//
// Progress streams to the activity queue via the shared trace-line handler:
// build/push/deploy each emit their `==> ...` umbrella lines, so the queue
// shows the same per-step progress it does for any other command.
func (a *App) runDeployOrchestration(ctx context.Context, selection uiSelection, result eruncommon.OpenResult, force bool) error {
	cli := a.deps.resolveCLIPath()
	dir := resolveTerminalStartDir(result.RepoPath)
	onLine := newActivityTraceLineHandler(a, selection, sessionKindLocal)

	if result.EnvConfig.BuildsHere() && !result.RemoteRepo() {
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
		_, err = runErunCaptured(ctx, cli, dir, onLine, deployArgs...)
		return err
	}

	args := []string{"deploy", selection.Tenant, selection.Environment}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		args = append(args, "--current")
	}
	if force {
		args = append(args, "--force")
	}
	_, err := runErunCaptured(ctx, cli, dir, onLine, args...)
	return err
}

// deployNeedsBuildOrchestration is the desktop's per-env-type deploy policy
// (root AGENTS.md § "Command primitives vs orchestration"): an Operator's
// deploy of a builds-here env whose source is on this machine (local-agent)
// means "build fresh, then publish, then install" — composed from the pure
// primitives — while a runtime env, or a builds-here env the operator pinned to
// a specific published version, means "install that version by reference". A
// forced deploy (the rebuild-&-redeploy recovery) always rebuilds. Remote-agent
// envs build in their pod, not through this local-machine orchestration, so
// their remote worktree excludes them here.
func deployNeedsBuildOrchestration(result eruncommon.OpenResult, version string, force bool) bool {
	if !result.EnvConfig.BuildsHere() || result.RemoteRepo() {
		return false
	}
	return force || strings.TrimSpace(version) == ""
}

// maybeStartDeployOrchestration starts the desktop's pure-primitive deploy
// orchestration for a builds-here agent env on this machine, returning
// (result, true) once the background run is launched. For runtime/remote envs
// it returns (_, false) so the caller falls back to the in-shell `erun deploy`
// path. The orchestration runs in the background; progress and failures surface
// through the activity queue via the streamed `==> ...` trace lines.
func (a *App) maybeStartDeployOrchestration(selection uiSelection, force bool) (startSessionResult, bool) {
	selection = normalizeSelection(selection)
	// a.ctx is nil in unit tests; never fall back to the machine's real CLI
	// and config there (the #492 hazard class).
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
		_ = a.runDeployOrchestration(a.activityWatcherCtx(), selection, result, force)
	}()
	return startSessionResult{Selection: selection, Orchestrated: true}, true
}

// runErunCaptured runs `erun <args>` in dir, streaming the trace stream
// (stderr — where erun's Logger writes its `==>` and trace lines) to onLine and
// capturing stdout, where `--output json` writes the structured result. Returns
// the captured stdout.
func runErunCaptured(ctx context.Context, cliPath, dir string, onLine func(string), args ...string) (string, error) {
	if strings.TrimSpace(cliPath) == "" {
		cliPath = "erun"
	}
	cmd := exec.CommandContext(ctx, cliPath, args...)
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
		if detail := strings.TrimSpace(lastErr.String()); detail != "" {
			return stdoutBuf.String(), fmt.Errorf("erun %s: %w: %s", args[0], err, detail)
		}
		return stdoutBuf.String(), fmt.Errorf("erun %s: %w", args[0], err)
	}
	return stdoutBuf.String(), nil
}

// scanErunStderr streams erun's trace stream line by line: every non-blank
// line is forwarded to onLine (the activity-queue trace handler) and every
// line is accumulated into lastErr (newline-separated) so the captured tail
// can be attached to a non-zero-exit error.
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

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// StartSession spawns the ERun tab's `erun open` PTY for (selection,
// slot). The work is queued through the per-(tenant,env) desktop action
// runner so a parallel AI tab open for the same env doesn't race a
// duplicate build+deploy. The Wails caller blocks until the session is
// created (or fails); the runner gate is released when the underlying
// `erun open` reaches its ready marker (==> Deployed / Defaulted
// container) or exits.
func (a *App) StartSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "open", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runOpenSession(ctx, selection, slot, cols, rows)
	})
}

// clampTerminalSize substitutes the default PTY geometry (120x34) for
// any non-positive cols/rows the Wails caller passed, so every session
// start path shares one fallback instead of repeating the guards.
func clampTerminalSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}
	return cols, rows
}

// runOpenSession is the original spawn logic for the ERun tab,
// wrapped so the desktop action runner can call it on its turn.
// Returns the result the Wails caller wants and the managedTerminal so
// the runner can wait on its ready signal.
func (a *App) runOpenSession(ctx context.Context, selection uiSelection, slot, cols, rows int) (startSessionResult, *managedTerminal, error) {
	cols, rows = clampTerminalSize(cols, rows)

	key := openSessionKey(selection, slot)
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		go a.startWorkspaceSyncForSelection(selection)
		// Reusing an existing session — already past setup, gate
		// can release immediately.
		existing.signalReady(nil)
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
		}, existing, nil
	}
	a.mu.Unlock()

	// One preflight per env (re)start (issue #463): the shared ensure runs
	// the open/build/deploy once and streams its traces into the activity
	// queue; the tab itself skips the preflight and waits on the deployment.
	a.ensureEnvRuntimeOnce(selection)
	openParams := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       append(withAppSession(buildOpenArgs(result.Tenant, result.Environment), fmt.Sprintf("open-%d", slot), false, false), "--skip-ensure"),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(openParams)
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:                session,
		selection:              selection,
		key:                    key,
		serial:                 serial,
		slot:                   slot,
		kind:                   sessionKindOpen,
		blocksIdleStop:         true,
		clearIdleBlockOnOutput: true,
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(openParams)
		},
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.busyEnvs[selectionKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	go a.streamSession(managed)
	go a.startWorkspaceSyncForSelection(selection)
	go a.startCloudCredentialsRefresherForSelection(selection)

	// A fresh open attempt supersedes any stopped/failed flag the row
	// carried; the reconnect refusal paths re-flag if this open fails too.
	a.emitEnvStatus(selection, "")
	a.logSpawnedCommandToLocal(selection, "erun", formatLocalCommandLog(formatLaunchCommand(openParams), "ERun tab"))
	_ = ctx
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindOpen),
	}, managed, nil
}

func (a *App) StartLocalSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	cols, rows = clampTerminalSize(cols, rows)

	key := localSessionKey(selection, slot)

	repoPath := ""
	if result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}); err == nil {
		repoPath = result.RepoPath
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
			Kind:      string(sessionKindLocal),
		}, nil
	}
	a.mu.Unlock()

	executable, args := resolveLocalShellCommand(goruntime.GOOS)
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(repoPath),
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return startSessionResult{}, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:   session,
		selection: selection,
		key:       key,
		serial:    serial,
		slot:      slot,
		kind:      sessionKindLocal,
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.mu.Unlock()

	if banner := localSessionBanner(selection); len(banner) > 0 {
		a.emitEvent(terminalOutputEvent, terminalOutputPayload{
			SessionID: serial,
			Data:      base64.StdEncoding.EncodeToString(banner),
		})
	}

	go a.streamSession(managed)
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindLocal),
	}, nil
}

// StartAISession spawns the AI tab's `erun open` PTY and pipes the
// configured AI tool's startup command into stdin. Same per-env queue
// gating as StartSession so an AI tab opened alongside an ERun tab
// doesn't trigger a duplicate build+deploy.
func (a *App) StartAISession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "ai", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runAISession(ctx, selection, slot, cols, rows)
	})
}

func (a *App) runAISession(ctx context.Context, selection uiSelection, slot, cols, rows int) (startSessionResult, *managedTerminal, error) {
	cols, rows = clampTerminalSize(cols, rows)

	key := aiSessionKey(selection, slot)
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		existing.signalReady(nil)
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
			Slot:      slot,
			Kind:      string(sessionKindAI),
		}, existing, nil
	}
	a.mu.Unlock()

	// Shared per-env ensure (issue #463) — see StartSession.
	a.ensureEnvRuntimeOnce(selection)
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		// The AI tab runs `erun open --app-session ai --ai`: the persistent
		// remote session launches the AI tool itself (the cwd-guarded claude
		// resume at the env effort, issues #451/#469), once on create. Reopening
		// reconnects to the running claude rather than typing it in again or
		// spawning a parallel one (#478).
		Args: append(withAppSession(buildOpenArgs(result.Tenant, result.Environment), "ai", true, false), "--skip-ensure"),
		Env:  []string{appSessionEnvVar + "=1"},
		Cols: cols,
		Rows: rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return startSessionResult{}, nil, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:   session,
		selection: selection,
		key:       key,
		serial:    serial,
		slot:      slot,
		kind:      sessionKindAI,
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(params)
		},
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.mu.Unlock()

	a.rememberKubeContextForActivity(selection.KubernetesContext)
	go a.streamSession(managed)

	a.logSpawnedCommandToLocal(selection, "ai", formatLocalCommandLog(formatLaunchCommand(params), "AI tab"))
	_ = ctx
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindAI),
	}, managed, nil
}

func (a *App) StartInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.runErunCommandInLocal(selection, cols, rows, buildInitArgs(selection))
}

func (a *App) StartDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	// Agent envs (builds-here, source on this machine) deploy fresh code: the
	// desktop composes the pure primitives — build -> push -> deploy, threading
	// the minted version — rather than the `build --deploy` operator shortcut
	// (erun-ui/AGENTS.md). Runtime/published-chart envs install a version by
	// reference and keep the in-shell `erun deploy` path below.
	if result, ok := a.maybeStartDeployOrchestration(selection, false); ok {
		return result, nil
	}
	// The PTY trace handler picks up `==> Deploying tenant/env <ver>`
	// from the Local tab and registers a deploy entry within milliseconds.
	// The helm release poller converges onto the same record by ID once
	// the cluster sees the pending release.
	return a.runErunCommandInLocal(selection, cols, rows, buildDeployArgs(selection))
}

// StartForceDeploySession runs `erun deploy --force` in the Local tab.
// Wails-exposed: bound to the "Rebuild & redeploy" affordance shown next
// to a failing container in the activity drawer when the kubelet error
// looks like a missing-image case (the registry doesn't have the chart's
// referenced tag yet, so a forced rebuild + push is the recovery path).
func (a *App) StartForceDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	if result, ok := a.maybeStartDeployOrchestration(selection, true); ok {
		return result, nil
	}
	args := append(buildDeployArgs(selection), "--force")
	return a.runErunCommandInLocal(selection, cols, rows, args)
}

// StartUpgradeEnvironmentSession runs `erun upgrade --tenant <t>
// --environment <e>` in that environment's own Local shell, so each
// Upgrade-all member upgrades in its respective env — output, activity
// entry, and any failure land on the env they belong to, and members run in
// parallel across envs (issue #497). The deploy emits the same
// `==> Deploying tenant/env` traces the activity-queue parser turns into
// entries, exactly like StartDeploySession.
func (a *App) StartUpgradeEnvironmentSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.runErunCommandInLocal(selection, cols, rows, buildUpgradeArgs(selection))
}

func (a *App) StartSSHDInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.runErunCommandInLocal(selection, cols, rows, buildSSHDInitArgs(selection))
}

func (a *App) StartDoctorSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.runErunCommandInLocal(selection, cols, rows, buildDoctorArgs(selection))
}

func (a *App) runErunCommandInLocal(selection uiSelection, cols, rows int, args []string) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}

	local, err := a.ensureLocalSession(selection, 0, cols, rows)
	if err != nil {
		return startSessionResult{}, err
	}

	a.mu.Lock()
	target := local.session
	a.mu.Unlock()
	if target == nil {
		return startSessionResult{}, fmt.Errorf("local session is not ready")
	}

	command := buildLocalErunCommand(a.deps.resolveCLIPath(), args)
	if _, err := io.WriteString(target, command); err != nil {
		return startSessionResult{}, err
	}

	a.recordTerminalActivity(selection)

	return startSessionResult{
		SessionID: local.serial,
		Selection: selection,
		Slot:      local.slot,
		Kind:      string(sessionKindLocal),
	}, nil
}

func (a *App) ensureLocalSession(selection uiSelection, slot, cols, rows int) (*managedTerminal, error) {
	key := localSessionKey(selection, slot)

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		return existing, nil
	}
	a.mu.Unlock()

	if _, err := a.StartLocalSession(selection, slot, cols, rows); err != nil {
		return nil, err
	}

	a.mu.Lock()
	managed := a.sessions[key]
	a.mu.Unlock()
	if managed == nil {
		return nil, fmt.Errorf("local session not registered after start")
	}
	return managed, nil
}

func buildLocalErunCommand(cliPath string, args []string) string {
	cliPath = strings.TrimSpace(cliPath)
	if cliPath == "" {
		cliPath = "erun"
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuoteIfNeeded(cliPath))
	for _, arg := range args {
		parts = append(parts, shellQuoteIfNeeded(arg))
	}
	return strings.Join(parts, " ") + "\n"
}

func shellQuoteIfNeeded(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if !shellQuoteSafeRune(r) {
			return shellQuote(value)
		}
	}
	return value
}

// shellQuoteSafePunct lists the punctuation runes that are safe to leave
// unquoted in a shell command word alongside alphanumerics.
const shellQuoteSafePunct = "-_./=+:@,"

// shellQuoteSafeRune reports whether r can appear unquoted in a shell
// command word. Anything outside this allow-list forces shellQuote.
func shellQuoteSafeRune(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune(shellQuoteSafePunct, r)
	}
}

func (a *App) OpenIDE(selection uiSelection, ide string) error {
	selection = normalizeSelection(selection)
	ide = strings.TrimSpace(ide)
	if selection.Tenant == "" || selection.Environment == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	if ide != "vscode" && ide != "intellij" {
		return fmt.Errorf("unsupported IDE %q", ide)
	}
	params, err := a.resolveOpenIDEParams(selection, ide)
	if err != nil {
		return err
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := a.deps.runIDECommand(ctx, params)
	if err == nil {
		return nil
	}
	return formatOpenIDEError(ide, output, err)
}

// resolveOpenIDEParams resolves the open target and builds the launch
// params for `open <ide>`. Local repos launch the IDE directly; remote
// repos go through `erun open` and require an sshd-enabled environment.
func (a *App) resolveOpenIDEParams(selection uiSelection, ide string) (startTerminalSessionParams, error) {
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startTerminalSessionParams{}, err
	}

	params := startTerminalSessionParams{
		Dir:        resolveDeployStartDir(a.deps.findProjectRoot, result),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildOpenIDEArgs(selection, ide),
		Env:        []string{appSessionEnvVar + "=1"},
	}
	if !result.RemoteRepo() {
		localParams, err := buildLocalOpenIDEParams(result, ide)
		if err != nil {
			return startTerminalSessionParams{}, err
		}
		params = localParams
		params.Env = []string{appSessionEnvVar + "=1"}
	} else if !result.EnvConfig.SSHD.Enabled {
		return startTerminalSessionParams{}, fmt.Errorf("open %s requires sshd-enabled remote environment; run `erun sshd init %s %s` first", ide, selection.Tenant, selection.Environment)
	}
	return params, nil
}

// formatOpenIDEError wraps a non-nil runIDECommand error with the IDE
// name, appending the trimmed command output as detail when present.
func formatOpenIDEError(ide, output string, err error) error {
	if detail := strings.TrimSpace(output); detail != "" {
		return fmt.Errorf("open %s: %w: %s", ide, err, detail)
	}
	return fmt.Errorf("open %s: %w", ide, err)
}

func (a *App) StartCloudInitAWSSession(cols, rows int) (startSessionResult, error) {
	cols, rows = clampTerminalSize(cols, rows)
	key := "cloud/init/aws"

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.mu.Unlock()
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
		}, nil
	}
	a.mu.Unlock()

	session, err := a.deps.startTerminal(startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(""),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildCloudInitAWSArgs(),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	})
	if err != nil {
		return startSessionResult{}, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:   session,
		key:       key,
		serial:    serial,
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.mu.Unlock()

	go a.streamSession(managed)

	return startSessionResult{SessionID: serial}, nil
}

func (a *App) DeleteEnvironment(selection uiSelection, confirmation string) (deleteEnvironmentResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return deleteEnvironmentResult{}, fmt.Errorf("tenant and environment are required")
	}
	expected := eruncommon.DeleteEnvironmentConfirmation(selection.Tenant, selection.Environment)
	if strings.TrimSpace(confirmation) != expected {
		return deleteEnvironmentResult{}, fmt.Errorf("delete confirmation did not match %q", expected)
	}

	store, ok := a.deps.store.(eruncommon.DeleteStore)
	if !ok {
		return deleteEnvironmentResult{}, fmt.Errorf("environment deletion is not supported by the configured store")
	}
	envConfig, _, err := store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	linkedContext, hasLinkedContext, err := a.ensureLinkedCloudContextRunning(envConfig)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}

	result, err := eruncommon.RunDeleteEnvironment(eruncommon.Context{}, eruncommon.DeleteEnvironmentParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}, store, a.deps.deleteNamespace)
	stopError := ""
	if hasLinkedContext {
		if _, stopErr := a.stopCloudContext(linkedContext.Name); stopErr != nil {
			stopError = stopErr.Error()
		}
	}
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	a.stopCloudCredentialsRefresherForSelection(selection)
	a.closeSessionsForSelection(selection)
	return deleteEnvironmentResult{
		Tenant:                result.Tenant,
		Environment:           result.Environment,
		Namespace:             result.Namespace,
		KubernetesContext:     result.KubernetesContext,
		NamespaceDeleteError:  result.NamespaceDeleteError,
		CloudContextStopError: stopError,
	}, nil
}

func (a *App) SendSessionInput(sessionID int, data string) error {
	if data == "" {
		return nil
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}

	if _, err := io.WriteString(managed.session, data); err != nil {
		return err
	}
	a.clearAwaitingPostRespawnInput(managed)
	a.recordTerminalActivity(managed.selection)
	return nil
}

// clearAwaitingPostRespawnInput marks the managed session as having
// received real user input, so subsequent output once again counts as
// activity in streamSession's 2s ticker.
func (a *App) clearAwaitingPostRespawnInput(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.awaitingPostRespawnInput = false
	a.mu.Unlock()
}

// isAwaitingPostRespawnInput reports whether the managed session was
// just respawned and has not yet received user input.
func (a *App) isAwaitingPostRespawnInput(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return managed.awaitingPostRespawnInput
}

func (a *App) recordTerminalActivity(selection uiSelection) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" || a.deps.recordActivity == nil {
		return
	}
	_ = a.deps.recordActivity(eruncommon.EnvironmentActivityParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Kind:        eruncommon.ActivityKindCLI,
	})
}

func (a *App) SavePastedImage(sessionID int, payload pastedImagePayload) (pastedImageResult, error) {
	data, mimeType, err := decodePastedImagePayload(payload)
	if err != nil {
		return pastedImageResult{}, err
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return pastedImageResult{}, fmt.Errorf("no active terminal session")
	}

	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      managed.selection.Tenant,
		Environment: managed.selection.Environment,
	})
	if err != nil {
		return pastedImageResult{}, err
	}

	path, err := a.deps.savePastedImage(pastedImageSaveParams{
		Result:   result,
		Data:     data,
		MIMEType: mimeType,
		Name:     payload.Name,
	})
	if err != nil {
		return pastedImageResult{}, err
	}
	return pastedImageResult{Path: path}, nil
}

func (a *App) LoadDiff(selection uiSelection, options uiDiffOptions) (eruncommon.DiffResult, error) {
	selection = normalizeSelection(selection)
	options.Scope = strings.TrimSpace(options.Scope)
	options.SelectedCommit = strings.TrimSpace(options.SelectedCommit)
	if selection.Tenant == "" || selection.Environment == "" {
		return eruncommon.DiffResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return eruncommon.DiffResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.canConnectLocalPort != nil && !a.deps.canConnectLocalPort(mcpPort) {
		return eruncommon.DiffResult{}, wrapMCPUnreachableError(fmt.Errorf("mcp port %d is not reachable", mcpPort))
	}
	endpoint := mcpEndpointForOpenResult(result)
	diff, err := a.deps.loadDiff(ctx, endpoint, options)
	if err != nil && isMCPDialFailure(err) {
		return eruncommon.DiffResult{}, wrapMCPUnreachableError(err)
	}
	return diff, err
}

func (a *App) ensureMCPAvailable(ctx context.Context, result eruncommon.OpenResult) error {
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.ensureMCP != nil && !a.deps.canConnectLocalPort(mcpPort) {
		if err := a.deps.ensureMCP(ctx, result); err != nil {
			if !a.deps.canConnectLocalPort(mcpPort) {
				return err
			}
		}
	}
	return nil
}

// ReconnectMCP runs `erun open --no-shell` for the selected environment so
// the local MCP port-forward (and, if the runtime pod is missing, the
// runtime itself) gets re-established. This is the only desktop path that
// may invoke the open command implicitly on the user's behalf, and it is
// gated on an explicit user click in the review panel's unreachable state.
//
// Stdout/stderr lines are streamed to the frontend via the
// mcpReconnectLineEvent so a long-running deploy does not look frozen.
func (a *App) ReconnectMCP(selection uiSelection) error {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if a.deps.reconnectMCP == nil {
		return fmt.Errorf("reconnect is not configured")
	}
	emit := func(line string) {
		a.emit(mcpReconnectLineEvent, line)
	}
	return a.deps.reconnectMCP(ctx, result, emit)
}

func (a *App) ResizeSession(sessionID, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}

	return managed.session.Resize(cols, rows)
}

func (a *App) CloseSession(sessionID int) error {
	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	var endRemote bool
	var endSelection uiSelection
	var endID string
	if managed != nil && managed.kind == sessionKindOpen && managed.slot > 0 && !managed.takenOver {
		// An explicit close of a custom terminal tab is the user removing
		// that terminal, not just detaching: end the pod session too, or
		// detection rebuilds the tab on the next env open. Default tabs stay
		// detach-only (their long-running sessions are the feature), and a
		// taken-over tab must never kill the session another window now owns.
		endRemote = true
		endSelection = managed.selection
		endID = fmt.Sprintf("open-%d", managed.slot)
	}
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}
	if endRemote {
		go a.endRemoteAppSession(endSelection, endID)
	}
	return managed.session.Close()
}

// CloseEnvironmentSessions tears down every managed PTY session
// bound to the supplied (tenant, environment) pair, regardless of
// the per-session debug flag, and returns the serial IDs that were
// closed so the frontend can drop them from tabsByEnv and related
// session bookkeeping in one round-trip.
//
// Used by the sidebar's "close env" affordance (the green dot next
// to an env name): the user clicked the dot to tear down the env's
// Local/ERun/AI tabs and stop the desktop from tracking the env.
// This is a desktop-only operation — it does NOT touch the cloud
// context's AWS state. See StopCloudContext for that.
//
// Collect targets under a.mu, release the lock, then call Close on
// each — session.Close may block on I/O and can call back into
// session bookkeeping that re-acquires a.mu, so holding the lock
// across the close would deadlock.
func (a *App) CloseEnvironmentSessions(selection uiSelection) ([]int, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	targets := a.collectLiveSessionsForSelection(selection)
	return closeManagedTerminals(targets)
}

// collectLiveSessionsForSelection gathers, under a.mu, every live
// (non-nil, not-yet-closed) managed PTY bound to the supplied
// (tenant, environment) pair. The lock is released before the caller
// closes them — see CloseEnvironmentSessions for the deadlock rationale.
func (a *App) collectLiveSessionsForSelection(selection uiSelection) []*managedTerminal {
	var targets []*managedTerminal
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, managed := range a.sessions {
		if managed == nil || managed.closed {
			continue
		}
		if managed.selection.Tenant != selection.Tenant {
			continue
		}
		if managed.selection.Environment != selection.Environment {
			continue
		}
		targets = append(targets, managed)
	}
	return targets
}

// closeManagedTerminals closes each target's underlying session and
// returns the serial IDs that closed cleanly along with the first close
// error encountered (if any). Targets with no session are skipped.
func closeManagedTerminals(targets []*managedTerminal) ([]int, error) {
	closed := make([]int, 0, len(targets))
	var firstErr error
	for _, managed := range targets {
		if managed.session == nil {
			continue
		}
		if err := managed.session.Close(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		closed = append(closed, managed.serial)
	}
	return closed, firstErr
}

// EndAISessions permanently ends the env's AI sessions — the env AI tab and
// the contribute AI tab, desktop side and pod side — so the next
// StartAISession / StartContributeAISession launches Claude with the env's
// current launch flags (--effort / --model / --verbose --debug, issues
// #477/#482). A launch flag only takes effect when the persistent session's
// create-time program runs: `dtach -A` reattaches to the running claude, so
// without ending the pod session a changed flag could never apply. The pod
// sessions are ended even when no desktop tab is attached — a detached claude
// would otherwise keep its stale flags and the next open would silently
// reattach to it. The relaunched guard resumes via --continue, so the Claude
// conversation carries over. Local/ERun tabs are untouched: their launch
// command does not change.
//
// Returns false without ending anything when the env's AI tool launches
// verbatim (a non-claude tool, or claude invoked with explicit flags): the
// managed Claude launch flags never participate in that launch, and ending
// would discard a session — codex has no --continue — for a setting that
// cannot affect it.
func (a *App) EndAISessions(selection uiSelection) (bool, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return false, fmt.Errorf("tenant and environment are required")
	}
	if envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment); err == nil {
		launch := eruncommon.AISessionLaunchCommand(envConfig.AITool, envConfig.Claude)
		if launch == strings.TrimSpace(envConfig.AITool) {
			return false, nil
		}
	}
	var targets []*managedTerminal
	a.mu.Lock()
	for _, managed := range a.sessions {
		if !managedAITabFor(managed, selection) {
			continue
		}
		// Mark closed under the lock so the spawn-reuse branch and
		// tryReconnect both refuse this session while it is torn down; the
		// frontend respawn that follows must create a fresh session, not
		// reattach to the dying one.
		managed.closed = true
		a.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	a.mu.Unlock()
	for _, managed := range targets {
		_ = managed.session.Close()
	}
	// End the pod-side persistent sessions only after the desktop PTYs are
	// gone: ending first would remove the owner file under an attached
	// client, which exits with the taken-over code and freezes the tab on
	// the takeover marker instead of relaunching.
	var wg sync.WaitGroup
	for _, sessionID := range []string{"ai", "contribute-ai"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			a.endRemoteAppSession(selection, id)
		}(sessionID)
	}
	wg.Wait()
	return true, nil
}

// managedAITabFor reports whether the managed session is one of the env's
// live AI tabs (env AI tab or contribute AI tab) that EndAISessions tears
// down for a Claude launch-flag change.
func managedAITabFor(managed *managedTerminal, selection uiSelection) bool {
	if managed == nil || managed.closed || managed.session == nil {
		return false
	}
	if managed.selection.Tenant != selection.Tenant ||
		managed.selection.Environment != selection.Environment {
		return false
	}
	return managed.kind == sessionKindAI || managed.kind == sessionKindContributeAI
}

func (a *App) sessionBySerialLocked(sessionID int) *managedTerminal {
	if sessionID <= 0 {
		return nil
	}
	for _, managed := range a.sessions {
		if managed != nil && managed.serial == sessionID {
			return managed
		}
	}
	return nil
}

func decodePastedImagePayload(payload pastedImagePayload) ([]byte, string, error) {
	value := strings.TrimSpace(payload.Data)
	mimeType := strings.TrimSpace(payload.MIMEType)
	if strings.HasPrefix(value, "data:") {
		header, body, ok := strings.Cut(value, ",")
		if !ok {
			return nil, "", fmt.Errorf("pasted image data URL is malformed")
		}
		value = body
		if mimeType == "" {
			mediaType := strings.TrimPrefix(header, "data:")
			mediaType, _, _ = strings.Cut(mediaType, ";")
			mimeType = strings.TrimSpace(mediaType)
		}
	}
	if value == "" {
		return nil, "", fmt.Errorf("pasted image data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("decode pasted image: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("pasted image data is empty")
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return nil, "", fmt.Errorf("clipboard item is not an image")
	}
	return data, mimeType, nil
}

func (a *App) streamSession(managed *managedTerminal) {
	buffer := make([]byte, 8192)
	var lastOutputActivity time.Time
	for {
		current := a.currentSessionFor(managed)
		if current == nil {
			return
		}
		count, err := current.Read(buffer)
		if count > 0 {
			a.handleSessionOutput(managed, buffer[:count], &lastOutputActivity)
		}
		if err == nil {
			continue
		}
		reason := terminalSessionExitReason(current, err)
		if a.tryReconnect(managed, reason) {
			continue
		}
		a.finalizeSessionExit(managed, reason)
		return
	}
}

// handleSessionOutput forwards one PTY read to the frontend, feeds the
// activity-trace parser and the AI-activity debounce, releases the idle
// block on first output, and refreshes the idle-activity marker no more
// than once every 2s. lastOutputActivity is advanced in place when the
// marker is refreshed.
func (a *App) handleSessionOutput(managed *managedTerminal, chunk []byte, lastOutputActivity *time.Time) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: managed.serial,
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
	a.feedActivityTraceFromTerminal(managed, chunk)
	a.recordAIActivity(managed)
	if managed.clearIdleBlockOnOutput {
		a.mu.Lock()
		a.releaseIdleBlockLocked(managed)
		a.mu.Unlock()
	}
	if time.Since(*lastOutputActivity) >= 2*time.Second {
		// While the session is waiting for the first user
		// input after a respawn, ignore the output ticker.
		// Reconnect noise (audit lines, "── reconnecting ──"
		// banners, the output of a respawned `erun open`
		// that fails again) must not refresh the idle
		// marker — only real interaction does. A real
		// keystroke clears the flag in SendSessionInput.
		if !a.isAwaitingPostRespawnInput(managed) {
			a.recordTerminalActivity(managed.selection)
			*lastOutputActivity = time.Now()
		}
	}
}

// finalizeSessionExit tears down a managed PTY that streamSession could
// not reconnect: it flips the AI busy latch off, removes the session
// from the registry, releases its idle block, emits the exit event,
// releases any action runner blocked on the ready signal, and kicks off
// the post-sshd-init workspace sync on a clean exit.
func (a *App) finalizeSessionExit(managed *managedTerminal, reason string) {
	a.finalizeAIActivity(managed)
	a.mu.Lock()
	managed.closed = true
	if existing := a.sessions[managed.key]; existing == managed {
		delete(a.sessions, managed.key)
	}
	a.releaseIdleBlockLocked(managed)
	a.mu.Unlock()
	a.emitEvent(terminalExitEvent, terminalExitPayload{
		SessionID: managed.serial,
		Reason:    reason,
	})
	// Release any action runner waiting on this session's ready
	// signal. If the session never reached its setup-complete
	// marker (e.g. process exited mid-build), the gate would
	// otherwise hold until the action's hard timeout.
	var readyErr error
	if reason != "" {
		readyErr = fmt.Errorf("%s", reason)
	}
	managed.signalReady(readyErr)
	if reason == "" && strings.HasPrefix(managed.key, "sshd-init\x00") {
		go a.startWorkspaceSyncForSelection(managed.selection)
	}
}

func (a *App) currentSessionFor(managed *managedTerminal) terminalSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	if managed == nil || managed.closed {
		return nil
	}
	return managed.session
}

// reconnectRefused reports whether tryReconnect should decline to
// respawn the managed PTY, emitting the matching terminal marker and env
// status as a side effect. Each guard is a terminal condition (handover,
// stopped cloud context, deploy failure, or a fast-exit loop) where an
// automatic respawn would fight another actor or storm a broken env; the
// user's recovery affordance is named in the emitted marker. A false
// return means none of the terminal conditions apply and the caller may
// respawn.
func (a *App) reconnectRefused(managed *managedTerminal) bool {
	// Another ERun window re-attached this persistent session — a
	// deliberate handover, not a transient drop. Respawning would run
	// `erun open` again, whose attach takes the session straight back,
	// and the two windows would steal it from each other in a loop.
	// Clicking the env in the sidebar is the deliberate take-back.
	if a.sessionTakenOver(managed) {
		a.emitTakenOverMarker(managed.serial)
		return true
	}

	// Refuse to respawn while the env's linked cloud context is not
	// running. Each respawn re-runs `erun open`, whose preflight calls
	// StartCloudContext and immediately undoes any auto-stop that has
	// just fired. Without this gate the desktop and the runtime-pod
	// monitor stop and start the EC2 instance in a tight loop, which
	// surfaces in the terminal as repeated IncorrectInstanceState
	// errors. End the loop cleanly here; the titlebar Play button is
	// the recovery affordance (same shape as the autoStart=never
	// empty state from #331).
	if !a.shouldRespawnForCloudContext(managed) {
		a.emitStoppedContextMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusStopped)
		return true
	}

	// A deploy failure is terminal, not a transient drop. Respawning
	// re-runs `erun open`, which re-deploys and fails the same way — and
	// because every env tab (ERun + AI) reconnects independently, that
	// turns one failing deploy into a parallel re-deploy storm across
	// tabs. Refuse here and leave recovery to the user (the failed-deploy
	// card's Run doctor / Rebuild & redeploy, or the titlebar Play
	// button). A session that reached a healthy shell and then dropped has
	// readyErr == nil and still reconnects below; its `erun open` finds the
	// deploy already current and skips it.
	if a.reconnectBlockedByDeployFailure(managed) {
		a.emitDeployFailedMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	// The readyErr guard above only catches an open that emitted the
	// `==> Deploy failed` trace. When the deploy failed in a *separate*
	// activity and this open is just trying to reach the resulting (never
	// ready) pod, it exits with an MCP port-forward timeout / "pod not ready
	// while syncing SSH" instead — no deploy-failed readyErr — and would loop
	// here forever (each respawn re-runs `erun open`, re-times-out, and stacks
	// another queued open). Consult the activity queue: if the env's latest
	// deploy failed, refuse for the same reason and leave recovery to the user.
	if a.reconnectBlockedByActivityDeployFailure(managed) {
		a.emitDeployFailedMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	// Cap consecutive fast-exit respawns. Without this, an env whose
	// underlying cluster keeps tearing down the freshly-spawned pod
	// (helm rollout timeouts, MCP port-forward races against a
	// terminating instance, etc.) loops indefinitely — each respawn
	// re-runs `erun open`, which deploys, fails again in seconds, and
	// exits. The user sees N stacked "Deploy failed after Ns" entries
	// in the activity drawer and a wall of "── reconnecting ──"
	// markers in the terminal. Stop after the cap and surface a
	// single explicit retry affordance. See issue #361.
	if a.trackExitForLoopGuard(managed) {
		a.emitReconnectLoopMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	return false
}

func (a *App) tryReconnect(managed *managedTerminal, exitReason string) bool {
	a.mu.Lock()
	if managed == nil || managed.closed || managed.respawn == nil {
		a.mu.Unlock()
		return false
	}
	respawn := managed.respawn
	a.mu.Unlock()

	if a.reconnectRefused(managed) {
		return false
	}

	a.emitReconnectMarker(managed.serial, exitReason)
	// The respawned `erun open` runs with --skip-ensure; refresh the shared
	// ensure (TTL-deduped) so a replaced pod gets its deploy back without
	// every tab repeating the preflight (issue #463).
	if managed.kind != sessionKindLocal {
		a.ensureEnvRuntimeOnce(managed.selection)
	}
	next, err := respawn()
	if err != nil {
		a.emitReconnectFailureMarker(managed.serial, err)
		return false
	}

	a.mu.Lock()
	if managed.closed {
		a.mu.Unlock()
		_ = next.Close()
		return false
	}
	managed.session = next
	managed.awaitingPostRespawnInput = true
	a.mu.Unlock()
	// The respawn went through — whatever stopped/failed condition the row
	// was flagged with is being retried, so clear it (the refusal paths
	// above re-flag on the next failure).
	a.emitEnvStatus(managed.selection, "")
	return true
}

// shouldRespawnForCloudContext returns true when the managed PTY can be
// safely relaunched. A managed terminal whose env has no linked cloud
// context (local envs) always reconnects; a managed cloud env reconnects
// only when the last-known context status is "running" or "pending"
// (start in flight). Anything else means the context is stopped or
// transitioning toward stopped, and an immediate respawn would fight
// the desktop's auto-stop. Best-effort: any error reading the store is
// treated as "allow respawn" so a transient store failure does not
// permanently break reconnect.
func (a *App) shouldRespawnForCloudContext(managed *managedTerminal) bool {
	if managed == nil || a.deps.store == nil {
		return true
	}
	// Honor an explicit Stop click before consulting on-disk state. The
	// status poller writes the cloud-context status on its own cadence,
	// so a fresh Stop leaves a wide race window where the on-disk status
	// still reads running while the kubectl session is already gone.
	// isIntentionalStop closes that window; the marker is cleared on
	// successful Start so the next reconnect cycle behaves normally.
	if a.isIntentionalStop(managed.selection) {
		return false
	}
	config, _, err := a.deps.store.LoadEnvConfig(managed.selection.Tenant, managed.selection.Environment)
	if err != nil {
		return true
	}
	cloudContext, ok, err := a.linkedCloudContext(config)
	if err != nil || !ok {
		return true
	}
	switch strings.TrimSpace(cloudContext.Status) {
	case eruncommon.CloudContextStatusRunning, eruncommon.CloudContextStatusPending:
		return true
	case "":
		return true
	default:
		return false
	}
}

func (a *App) logSpawnedCommandToLocal(selection uiSelection, dedupKey, line string) {
	a.mu.Lock()
	var local *managedTerminal
	for _, m := range a.sessions {
		if m == nil || m.closed || m.kind != sessionKindLocal {
			continue
		}
		if normalizeSelection(m.selection) != selection {
			continue
		}
		local = m
		break
	}
	if local == nil {
		a.mu.Unlock()
		return
	}
	if local.loggedCommands == nil {
		local.loggedCommands = make(map[string]struct{})
	}
	if _, ok := local.loggedCommands[dedupKey]; ok {
		a.mu.Unlock()
		return
	}
	local.loggedCommands[dedupKey] = struct{}{}
	serial := local.serial
	a.mu.Unlock()
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: serial,
		Data:      base64.StdEncoding.EncodeToString([]byte(line)),
	})
}

func (a *App) emitReconnectMarker(sessionID int, exitReason string) {
	suffix := ""
	if reason := strings.TrimSpace(exitReason); reason != "" {
		suffix = " " + reason
	}
	marker := "\r\n\x1b[2;33m── reconnecting" + suffix + " ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// reconnectLoopWindow and reconnectLoopMaxExits define the fast-exit
// loop guard: once the managed PTY has logged more than
// reconnectLoopMaxExits exits inside reconnectLoopWindow,
// tryReconnect stops respawning and surfaces the retry marker
// instead. The numbers are picked to absorb a couple of legitimate
// transient blips (an `erun open` retrying past a momentary AWS
// describe-instances error, for example) without leaving the user
// staring at a terminal of stacked reconnect noise. See issue #361.
const (
	reconnectLoopWindow     = 30 * time.Second
	reconnectLoopMaxExits   = 2
	reconnectLoopMarkerANSI = "\r\n\x1b[2;33m── stopped reconnecting after repeated failures — click the environment in the sidebar to retry ──\x1b[0m\r\n"
	deployFailedMarkerANSI  = "\r\n\x1b[2;33m── deploy failed — not retrying automatically; use Run doctor or Rebuild & redeploy on the failed deploy, or click the environment in the sidebar to retry ──\x1b[0m\r\n"
	takenOverMarkerANSI     = "\r\n\x1b[2;33m── session re-attached in another ERun window — click the environment in the sidebar to attach it here ──\x1b[0m\r\n"
)

// trackExitForLoopGuard records the moment the managed PTY exited
// and reports whether the recent-exit count has crossed the cap.
// Returns true when the caller (tryReconnect) should refuse respawn.
// Entries older than reconnectLoopWindow are pruned on each call so
// a single exit after a long-running session does not trip the cap.
func (a *App) trackExitForLoopGuard(managed *managedTerminal) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if managed == nil {
		return false
	}
	now := time.Now()
	cutoff := now.Add(-reconnectLoopWindow)
	kept := managed.recentExits[:0]
	for _, t := range managed.recentExits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	managed.recentExits = kept
	return len(kept) > reconnectLoopMaxExits
}

// emitReconnectLoopMarker writes a single diagnostic line when
// tryReconnect refuses to respawn because the managed PTY has been
// failing repeatedly in a short window. The dim-yellow style matches
// the reconnecting and stopped-context markers so the user reads them
// as the same status channel. The recovery action is named in the
// line — re-click the env in the sidebar to retry.
func (a *App) emitReconnectLoopMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(reconnectLoopMarkerANSI)),
	})
}

// emitDeployFailedMarker writes a single diagnostic line when tryReconnect
// refuses to respawn because the env's deploy failed (rather than a transient
// drop). See tryReconnect.
func (a *App) emitDeployFailedMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(deployFailedMarkerANSI)),
	})
}

// emitTakenOverMarker writes a single diagnostic line when tryReconnect
// refuses to respawn because another ERun window re-attached the session.
// The named recovery action mirrors the other markers: clicking the env in
// the sidebar deliberately takes the session back.
func (a *App) emitTakenOverMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(takenOverMarkerANSI)),
	})
}

// markSessionTakenOver flags the managed PTY whose output carried the CLI's
// taken-over notice (eruncommon.ShellSessionTakenOverNotice); see the
// takenOver field for the semantics.
func (a *App) markSessionTakenOver(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.takenOver = true
	a.mu.Unlock()
}

func (a *App) sessionTakenOver(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return managed.takenOver
}

// reconnectBlockedByDeployFailure reports whether the managed PTY's open
// ended in a deploy failure — signalReady was called with an error from the
// `==> Deploy failed` trace line — as opposed to a healthy ready that later
// dropped. tryReconnect uses it to avoid re-deploying a broken env (and the
// parallel re-deploy storm that results when every tab reconnects at once).
func (a *App) reconnectBlockedByDeployFailure(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	managed.readyMu.Lock()
	defer managed.readyMu.Unlock()
	return managed.readyClosed && managed.readyErr != nil
}

// reconnectBlockedByActivityDeployFailure reports whether the managed PTY's env
// has a failed deploy recorded in the activity queue. Unlike
// reconnectBlockedByDeployFailure (which keys on this open's own
// `==> Deploy failed` readyErr), this catches the case where the deploy failed
// in a separate activity and the current open merely can't reach the resulting
// pod — so reconnect still stops hammering the broken env. Cleared once a later
// deploy succeeds (recovery), letting reconnect resume.
func (a *App) reconnectBlockedByActivityDeployFailure(managed *managedTerminal) bool {
	if managed == nil || a.activityQueue == nil {
		return false
	}
	return a.activityQueue.latestDeployFailed(managed.selection.Tenant, managed.selection.Environment)
}

// emitStoppedContextMarker writes a single diagnostic line when
// tryReconnect refuses to respawn because the env's cloud context is
// not running. The dim-yellow style matches the reconnecting marker so
// the user reads them as the same status channel; the recovery path
// (titlebar Play button) is named in the line so no separate UI
// element is needed.
func (a *App) emitStoppedContextMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click the start button in the titlebar to resume ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

func (a *App) emitReconnectFailureMarker(sessionID int, err error) {
	if err == nil {
		return
	}
	marker := "\r\n\x1b[31m── reconnect failed: " + err.Error() + " ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// aiActivitySustainedThreshold is how long the AI session must keep
// producing output before we flip the sidebar busy badge on. Chosen
// large enough to suppress single-line Codex responses (~1 s of output)
// while still catching real multi-second Claude generations.
const aiActivitySustainedThreshold = 5 * time.Second

// aiActivityIdleThreshold is how long the AI session must be silent
// before we flip the busy badge back off. Codex's "thinking..." spinner
// updates in bursts; this window swallows the gaps between bursts so
// the sidebar does not flicker mid-generation.
const aiActivityIdleThreshold = 3 * time.Second

// recordAIActivity is called from streamSession on every output read for
// every managed terminal; it only does work for sessionKindAI sessions.
// It implements the debounced "AI tab is working" signal described in
// erun-ui/AGENTS.md: Nielsen #1 (visibility of system status) requires
// the sidebar to show, at a glance, which env's AI tab is producing
// output — even when the user has navigated to a different env.
//
// Policy:
//   - busy=true fires after aiActivitySustainedThreshold of sustained
//     output (5 s), where "sustained" means continued output with gaps
//     no longer than aiActivityIdleThreshold (3 s). Short single-burst
//     responses do not toggle the badge.
//   - busy=false fires after aiActivityIdleThreshold (3 s) of silence.
//   - Session close emits busy=false via finalizeAIActivity.
func (a *App) recordAIActivity(managed *managedTerminal) {
	if managed == nil || managed.kind != sessionKindAI {
		return
	}
	now := time.Now()
	a.mu.Lock()
	if managed.closed {
		a.mu.Unlock()
		return
	}
	if managed.aiActiveSince.IsZero() || now.Sub(managed.aiLastOutput) > aiActivityIdleThreshold {
		managed.aiActiveSince = now
	}
	managed.aiLastOutput = now
	shouldFireBusy := !managed.aiBusyEmitted && now.Sub(managed.aiActiveSince) >= aiActivitySustainedThreshold
	if shouldFireBusy {
		managed.aiBusyEmitted = true
	}
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
	}
	managed.aiInactivityTimer = time.AfterFunc(aiActivityIdleThreshold, func() {
		a.clearAIActivityIfQuiet(managed)
	})
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	if shouldFireBusy {
		a.emitAIActivity(serial, selection, true)
	}
}

// clearAIActivityIfQuiet fires from the AfterFunc scheduled by
// recordAIActivity. If no new output has arrived in the meantime it
// clears the busy latch and emits ai-activity busy=false. If new output
// did arrive, the more recent recordAIActivity call already reset the
// timer; this firing is stale and a no-op.
func (a *App) clearAIActivityIfQuiet(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	if managed.closed || !managed.aiBusyEmitted {
		a.mu.Unlock()
		return
	}
	if time.Since(managed.aiLastOutput) < aiActivityIdleThreshold {
		a.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	a.emitAIActivity(serial, selection, false)
}

// finalizeAIActivity ensures the sidebar busy latch is released when an
// AI session exits while busy=true is in flight (e.g. user closes the
// tab mid-generation, or the underlying PTY drops). Caller must not
// hold a.mu.
func (a *App) finalizeAIActivity(managed *managedTerminal) {
	if managed == nil || managed.kind != sessionKindAI {
		return
	}
	a.mu.Lock()
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
		managed.aiInactivityTimer = nil
	}
	if !managed.aiBusyEmitted {
		a.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	a.emitAIActivity(serial, selection, false)
}

func (a *App) emitAIActivity(sessionID int, selection uiSelection, busy bool) {
	a.emitEvent(aiActivityEvent, aiActivityPayload{
		SessionID:   sessionID,
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Busy:        busy,
	})
}

// emitEnvStatus publishes the env's real condition for the sidebar row
// (issue #470). Status "" clears; envStatusStopped / envStatusFailed flag
// the row while its tabs are alive but the env is not actually running.
func (a *App) emitEnvStatus(selection uiSelection, status string) {
	a.emitEvent(envStatusEvent, envStatusPayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Status:      status,
	})
}

func terminalSessionExitReason(session terminalSession, readErr error) string {
	if session != nil {
		if waitErr := session.Wait(); waitErr != nil {
			return waitErr.Error()
		}
		return ""
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr.Error()
	}
	return ""
}

func (a *App) emitEvent(name string, payload any) {
	a.emit(name, payload)
}

func (a *App) closeAllSessionsLocked() {
	for _, session := range a.sessions {
		if session == nil {
			continue
		}
		_ = session.Close()
	}
	a.sessions = make(map[string]*managedTerminal)
}

func (a *App) closeSessionsForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	prefixes := []string{
		selectionKey(selection) + "\x00",
		"init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"local\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"ai\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, session := range a.sessions {
		if session == nil {
			continue
		}
		matches := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		_ = session.Close()
		delete(a.sessions, key)
	}
}

type managedTerminal struct {
	session                terminalSession
	selection              uiSelection
	key                    string
	serial                 int
	slot                   int
	kind                   sessionKind
	closed                 bool
	blocksIdleStop         bool
	clearIdleBlockOnOutput bool
	respawn                func() (terminalSession, error)
	loggedCommands         map[string]struct{}
	lockedByActivity       string
	activityTraceBuffer    string
	startedAt              time.Time

	// awaitingPostRespawnInput, when true, tells streamSession to skip
	// the 2s output-activity ticker. Set on each successful respawn so
	// reconnect noise (audit lines, "── reconnecting ──" banners, the
	// failure output of a respawned `erun open` that immediately fails
	// again) cannot count as user activity and refresh the idle marker.
	// Cleared on real user input — once the user types into the
	// reattached session, subsequent output is treated as work again.
	awaitingPostRespawnInput bool

	// recentExits records the wall-clock timestamps of recent PTY
	// exits for this managed terminal. tryReconnect uses it to break
	// out of fast-exit loops where the underlying `erun open`
	// (re)spawn keeps failing within seconds — a transitional EC2,
	// an unhealthy cluster, or a helm rollout that times out. Entries
	// older than reconnectLoopWindow are pruned on each tryReconnect
	// call, so a long-running successful session followed by a single
	// exit never trips the cap. See issue #361.
	recentExits []time.Time

	// takenOver is set when the session's output carries the CLI's
	// taken-over notice: another ERun window re-attached this persistent
	// pod session (screen-style detach-and-reattach). tryReconnect must
	// then refuse to respawn — respawning would steal the session straight
	// back and the two windows would fight. Clicking the env in the
	// sidebar starts a fresh session, which is the deliberate take-back.
	takenOver bool

	// aiActiveSince / aiLastOutput / aiBusyEmitted / aiInactivityTimer
	// drive the debounced AI activity signal that powers the sidebar
	// "Claude is working" spinner. Only populated for sessionKindAI
	// managed terminals. See recordAIActivity for the debounce policy
	// (5 s sustained output to flip on, 3 s silence to flip off).
	aiActiveSince     time.Time
	aiLastOutput      time.Time
	aiBusyEmitted     bool
	aiInactivityTimer *time.Timer

	// readyMu / readyCh / readyErr / readyClosed track the
	// "session is past its setup phase" signal. The desktop action
	// runner blocks on waitReady so the per-env queue gate releases
	// only when the underlying `erun open` has finished its build +
	// deploy work (or fast-pathed straight to kubectl-exec). Detected
	// by feedActivityTraceFromTerminal observing one of:
	//   ==> Deployed / ==> Deploy failed / ==> Skipping
	//   Defaulted container "..."
	// or the session closing.
	readyMu     sync.Mutex
	readyCh     chan struct{}
	readyErr    error
	readyClosed bool
}

// waitReady blocks until the session signals it has reached an
// interactive-ready state, the underlying process exits, ctx is
// cancelled, or `timeout` elapses (use 0 for "no timeout"). Returns
// nil on success, ctx.Err() on cancellation, the session's exit error
// on premature close, or context.DeadlineExceeded on timeout.
func (m *managedTerminal) waitReady(ctx context.Context, timeout time.Duration) error {
	if m == nil {
		return nil
	}
	m.readyMu.Lock()
	if m.readyCh == nil {
		m.readyCh = make(chan struct{})
	}
	ch := m.readyCh
	closed := m.readyClosed
	storedErr := m.readyErr
	m.readyMu.Unlock()
	if closed {
		return storedErr
	}
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ch:
		m.readyMu.Lock()
		defer m.readyMu.Unlock()
		return m.readyErr
	case <-ctx.Done():
		return ctx.Err()
	case <-timer:
		return context.DeadlineExceeded
	}
}

// signalReady marks the session ready exactly once. Subsequent calls
// are no-ops, which is the right behaviour: the first observed
// terminal-state line is authoritative.
func (m *managedTerminal) signalReady(err error) {
	if m == nil {
		return
	}
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	if m.readyClosed {
		return
	}
	if m.readyCh == nil {
		m.readyCh = make(chan struct{})
	}
	m.readyErr = err
	m.readyClosed = true
	close(m.readyCh)
}

type sessionKind string

const (
	sessionKindOpen    sessionKind = "erun"
	sessionKindLocal   sessionKind = "local"
	sessionKindAI      sessionKind = "ai"
	sessionKindCommand sessionKind = "command"
)

func (s *managedTerminal) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	s.closed = true
	return s.session.Close()
}

func selectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return selection.Tenant + "\x00" + selection.Environment
}

func openSessionKey(selection uiSelection, slot int) string {
	return selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func localSessionKey(selection uiSelection, slot int) string {
	return "local\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func aiSessionKey(selection uiSelection, slot int) string {
	return "ai\x00" + selectionKey(selection) + "\x00" + fmt.Sprintf("%d", slot)
}

func (a *App) releaseIdleBlockLocked(managed *managedTerminal) {
	if managed == nil || !managed.blocksIdleStop {
		return
	}
	busyKey := selectionKey(managed.selection)
	if a.busyEnvs[busyKey] <= 1 {
		delete(a.busyEnvs, busyKey)
	} else {
		a.busyEnvs[busyKey]--
	}
	managed.blocksIdleStop = false
	managed.clearIdleBlockOnOutput = false
}

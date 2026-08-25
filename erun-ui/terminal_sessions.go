package main

import (
	"bytes"
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

// StartSession spawns the ERun tab's `erun open` PTY, queued through the
// per-(tenant,env) action runner so a parallel AI-tab open for the same env
// cannot race a duplicate build+deploy. The Wails caller blocks until the
// session is created or fails.
func (a *App) StartSession(selection uiSelection, slot, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "open", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runOpenSession(ctx, selection, slot, cols, rows)
	})
}

func clampTerminalSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}
	return cols, rows
}

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

	// open is a pure primitive: the tab opens the shell against the
	// already-deployed runtime. Deploy is the caller's job (create / Deploy
	// button), never a side effect of opening a tab.
	a.ensureEnvRuntimeOnce(selection)
	openParams := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       withAppSession(buildOpenArgs(result.Tenant, result.Environment), fmt.Sprintf("open-%d", slot), false, false),
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
		appSession:             fmt.Sprintf("open-%d", slot),
		blocksIdleStop:         true,
		clearIdleBlockOnOutput: true,
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(reconnectSessionParams(openParams))
		},
		startedAt: time.Now(),
	}
	a.sessions[key] = managed
	a.busyEnvs[selectionKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	a.spawnStreamSession(managed)
	go a.startWorkspaceSyncForSelection(selection)
	go a.startCloudCredentialsRefresherForSelection(selection)

	// A fresh open attempt supersedes any stopped/failed flag the row
	// carried; the reconnect refusal paths re-flag if this open fails too.
	// Opening is also the deliberate wake for a runtime the operator stopped —
	// `erun open` scales it back up — so the stop latch is released here or the
	// reconnect gate would keep refusing against an env that is coming back.
	a.clearRuntimeStopped(selection)
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

	a.spawnStreamSession(managed)
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindLocal),
	}, nil
}

// StartAISession spawns the AI tab's `erun open` PTY under the same per-env
// queue gating as StartSession, so an AI tab opened alongside an ERun tab
// cannot trigger a duplicate build+deploy.
//
// A fresh (non-reattach) start checks the environment's activity leases first:
// this desktop's own AI/ERun/Local sessions never take one, so any held lease
// names a job (an orchestrator or CLI agent) already competing for the same
// pod's CPU, memory and disk. Unless confirmed is true, that start is reported
// back as occupied rather than launched, so starting a second agent stays a
// deliberate choice instead of a silent one.
func (a *App) StartAISession(selection uiSelection, slot, cols, rows int, confirmed bool) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	return a.enqueueGatedSession(selection, "ai", func(ctx context.Context) (startSessionResult, *managedTerminal, error) {
		return a.runAISession(ctx, selection, slot, cols, rows, confirmed)
	})
}

func (a *App) runAISession(ctx context.Context, selection uiSelection, slot, cols, rows int, confirmed bool) (startSessionResult, *managedTerminal, error) {
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

	if !confirmed {
		if occupants := a.aiSessionOccupants(selection); len(occupants) > 0 {
			return startSessionResult{
				Selection: selection,
				Slot:      slot,
				Kind:      string(sessionKindAI),
				Occupancy: occupants,
			}, nil, nil
		}
	}

	a.ensureEnvRuntimeOnce(selection)
	params := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		// The persistent pod session launches the AI tool itself, once on
		// create; reopening reattaches to the running tool rather than launching
		// a parallel one.
		Args: withAppSession(buildOpenArgs(result.Tenant, result.Environment), "ai", true, false),
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
		session:    session,
		selection:  selection,
		key:        key,
		serial:     serial,
		slot:       slot,
		kind:       sessionKindAI,
		appSession: "ai",
		respawn: func() (terminalSession, error) {
			return a.deps.startTerminal(reconnectSessionParams(params))
		},
		startedAt: time.Now(),
		lastCols:  cols,
		lastRows:  rows,
	}
	a.sessions[key] = managed
	a.mu.Unlock()

	a.rememberKubeContextForActivity(selection.KubernetesContext)
	a.spawnStreamSession(managed)

	a.logSpawnedCommandToLocal(selection, "ai", formatLocalCommandLog(formatLaunchCommand(params), "AI tab"))
	_ = ctx
	return startSessionResult{
		SessionID: serial,
		Selection: selection,
		Slot:      slot,
		Kind:      string(sessionKindAI),
	}, managed, nil
}

// aiSessionOccupants reuses the same idle-status resolution the titlebar polls
// (local-vs-remote, merged) to read the environment's held leases. A failed
// read fails open — returning no occupants rather than blocking the start —
// because this is a best-effort notice, not an access check.
func (a *App) aiSessionOccupants(selection uiSelection) []uiEnvironmentLease {
	status, err := a.LoadIdleStatus(selection)
	if err != nil {
		return nil
	}
	return status.Leases
}

func (a *App) StartInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	// init owns the env's single deploy, so it must carry the desktop's MCP-auth
	// key like the deploy paths do — otherwise the desktop would have to redeploy
	// after init to inject it, rolling the pod init just created.
	return a.runErunCommandInLocal(selection, cols, rows, a.appendMCPAuthPublicKeyFlag(buildInitArgs(selection)))
}

// StartDeploySession runs the pure `erun deploy` primitive: it installs an
// already-published version by reference and NEVER builds. Producing a new
// version from working-tree source is the explicit StartCreateVersionSession
// action — the Deploy button consumes a version, it does not produce one
// (erun-ui/AGENTS.md: the desktop composes pure primitives). With no picked
// version buildDeployArgs uses --current, i.e. redeploy the env's current
// version.
func (a *App) StartDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.runErunCommandInLocal(selection, cols, rows, a.appendMCPAuthPublicKeyFlag(buildDeployArgs(selection)))
}

// StartCreateVersionSession is the explicit "create & deploy new version"
// action: it builds the env's working tree into a fresh version, pushes it, and
// deploys it (build -> push -> deploy). Only a local-agent env has local source
// to build; a runtime/consumer env produces nothing, so this errors and the
// operator deploys a published version instead. The env-create flow's first
// deploy runs through here too.
func (a *App) StartCreateVersionSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if a.ctx == nil || strings.TrimSpace(selection.Tenant) == "" || strings.TrimSpace(selection.Environment) == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}
	if !result.EnvConfig.BuildsHere() || result.RemoteRepo() {
		return startSessionResult{}, fmt.Errorf("%s/%s has no local source to build; deploy a published version instead", selection.Tenant, selection.Environment)
	}
	go func() {
		_ = a.runDeployOrchestration(a.activityWatcherCtx(), selection, result, false)
	}()
	return startSessionResult{Selection: normalizeSelection(selection), Orchestrated: true}, nil
}

// StartInitialDeploySession stands a freshly-created env up in one step: a
// builds-here env builds+pushes+deploys its first version, while a
// runtime/consumer env installs its configured version by reference. Creating an
// env is itself the explicit "produce a version" act, so building here is not
// the implicit-build the plain Deploy button avoids.
func (a *App) StartInitialDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	if result, ok := a.maybeStartDeployOrchestration(selection, false); ok {
		return result, nil
	}
	return a.runErunCommandInLocal(selection, cols, rows, a.appendMCPAuthPublicKeyFlag(buildDeployArgs(selection)))
}

// StartForceDeploySession runs `erun deploy --force`, the "Rebuild & redeploy"
// recovery offered when a failing container looks like a missing-image case:
// the registry lacks the chart's referenced tag, so a forced rebuild + push is
// what fixes it.
func (a *App) StartForceDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	if result, ok := a.maybeStartDeployOrchestration(selection, true); ok {
		return result, nil
	}
	args := a.appendMCPAuthPublicKeyFlag(append(buildDeployArgs(selection), "--force"))
	return a.runErunCommandInLocal(selection, cols, rows, args)
}

// StartUpgradeEnvironmentSession runs `erun upgrade` in each environment's own
// Local shell, so an Upgrade-all fans out across envs in parallel and each
// env's output, activity entry, and failures land on the env they belong to.
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
	return buildLocalErunCommandForOS(goruntime.GOOS, cliPath, args)
}

func buildLocalErunCommandForOS(goos, cliPath string, args []string) string {
	cliPath = strings.TrimSpace(cliPath)
	if cliPath == "" {
		cliPath = "erun"
	}
	if goos == "windows" {
		// The Windows local shell is PowerShell (see resolveLocalShellCommand), so
		// the command must be PowerShell syntax: run the quoted exe via the call
		// operator (&) — a quoted path alone is just a string literal — and submit
		// with a carriage return. PSReadLine treats a bare LF as a line
		// continuation, which left the piped command stuck at the ">>" prompt and
		// never ran (so init/deploy/sshd/doctor silently did nothing).
		parts := make([]string, 0, len(args)+2)
		parts = append(parts, "&", powerShellQuoteIfNeeded(cliPath))
		for _, arg := range args {
			parts = append(parts, powerShellQuoteIfNeeded(arg))
		}
		return strings.Join(parts, " ") + "\r"
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuoteIfNeeded(cliPath))
	for _, arg := range args {
		parts = append(parts, shellQuoteIfNeeded(arg))
	}
	return strings.Join(parts, " ") + "\n"
}

// powerShellQuoteIfNeeded quotes value for PowerShell only when it contains a
// character that isn't safe bare (matching shellQuoteIfNeeded's safe set), so
// readable tokens like --type=remote-agent stay unquoted while paths with
// backslashes/colons are single-quoted.
func powerShellQuoteIfNeeded(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if !shellQuoteSafeRune(r) {
			return powerShellQuote(value)
		}
	}
	return value
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

const shellQuoteSafePunct = "-_./=+:@,"

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

	a.spawnStreamSession(managed)

	return startSessionResult{SessionID: serial}, nil
}

// StartCloudInitCloudflareSession opens a PTY running `erun cloud init
// cloudflare`. The CLI owns Cloudflare alias creation end-to-end; the desktop
// only launches the guided flow, so there is no in-app form.
func (a *App) StartCloudInitCloudflareSession(cols, rows int) (startSessionResult, error) {
	cols, rows = clampTerminalSize(cols, rows)
	key := "cloud/init/cloudflare"

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
		Args:       buildCloudInitCloudflareArgs(),
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

	a.spawnStreamSession(managed)

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

	// A later re-create of this env must fire environment-initialized again.
	a.clearInitEmitted(selection.Tenant, selection.Environment)
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
	a.noteSessionInput(managed)
	a.clearAwaitingPostRespawnInput(managed)
	a.recordTerminalActivity(managed.selection)
	if id, ok := orchestratorIDFromSessionKey(managed.key); ok {
		// Real operator input into the pane is the other rearm the pacing
		// nudge cap names: the operator is plainly at the keyboard, whatever
		// the last report said.
		a.rearmOrchestratorPacing(id)
	}
	return nil
}

func (a *App) clearAwaitingPostRespawnInput(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.awaitingPostRespawnInput = false
	a.mu.Unlock()
}

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

// SavePastedFile copies a file pasted into the desktop terminal into the env's
// runtime pod and returns its in-pod path. Any file type is accepted, not just
// images.
func (a *App) SavePastedFile(sessionID int, payload pastedFilePayload) (pastedFileResult, error) {
	data, mimeType, err := decodePastedFilePayload(payload)
	if err != nil {
		return pastedFileResult{}, err
	}

	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return pastedFileResult{}, fmt.Errorf("no active terminal session")
	}

	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      managed.selection.Tenant,
		Environment: managed.selection.Environment,
	})
	if err != nil {
		return pastedFileResult{}, err
	}

	path, err := a.deps.savePastedFile(pastedFileSaveParams{
		Result:   result,
		Data:     data,
		MIMEType: mimeType,
		Name:     payload.Name,
	})
	if err != nil {
		return pastedFileResult{}, err
	}
	return pastedFileResult{Path: path}, nil
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
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return eruncommon.DiffResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(eruncommon.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	diff, err := a.deps.loadDiff(ctx, endpoint, bearer, options)
	if err != nil && isMCPDialFailure(err) {
		return eruncommon.DiffResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
	}
	return diff, err
}

// classifyMCPUnreachable reports which locally observable shape of
// unreachability the review panel is looking at, through the same injectable
// port-bound check the rest of erun-ui uses (so it stays testable without a
// real dial).
func (a *App) classifyMCPUnreachable(port int) eruncommon.LocalMCPUnreachableKind {
	if a.deps.canConnectLocalPort != nil && a.deps.canConnectLocalPort(port) {
		return eruncommon.LocalMCPStaleForward
	}
	return eruncommon.LocalMCPNotOpen
}

func (a *App) ensureMCPAvailable(ctx context.Context, result eruncommon.OpenResult) error {
	mcpPort := eruncommon.MCPPortForResult(result)
	// Reachability is a round trip, not a dial. A stale forward holds the port
	// and answers nothing, so gating recovery on a dial left the env
	// permanently unreachable behind a listener that looked fine.
	if a.deps.ensureMCP != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		a.emitAppStatus(eruncommon.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort), false)
		if err := a.deps.ensureMCP(ctx, result); err != nil {
			if !a.deps.canReachMCPEndpoint(mcpPort) {
				return err
			}
		}
	}
	return nil
}

// ReconnectMCP runs `erun open --no-shell` to re-establish the env's MCP
// port-forward (and the runtime pod itself if it is gone). This is the only
// desktop path that opens implicitly on the user's behalf, and it is gated on
// an explicit click in the review panel's unreachable state. Output is streamed
// to the frontend so a long-running deploy does not look frozen.
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
	if managed != nil {
		managed.lastCols = cols
		managed.lastRows = rows
	}
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}

	return managed.session.Resize(cols, rows)
}

// RepaintSession forces the session's program to fully repaint by raising a real
// WINCH (shrink one row, then restore) on its backend pty. Switching to a tab
// replays the retained buffer locally, but an Ink/alt-screen TUI (Claude) only
// repaints on a geometry change, so a same-size switch would leave the tab blank
// until the app next emits a diff on its own. Unlike maybeNudgeAIRepaint this is
// deliberately NOT gated by repaintNudged (that guard is for the once-per-attach
// streaming path — every alt-screen switch wants a fresh repaint); the frontend
// calls it only for alt-screen sessions. The local xterm is never resized, so
// the user sees the frame appear with no visible reflow.
func (a *App) RepaintSession(sessionID int) error {
	a.mu.Lock()
	managed := a.sessionBySerialLocked(sessionID)
	var cols, rows int
	var gen uint64
	var typedRecently bool
	if managed != nil {
		cols, rows = managed.lastCols, managed.lastRows
		gen = managed.inputGen
		typedRecently = typedRecentlyLocked(managed)
	}
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}
	if typedRecently {
		// Switching into a pane fires this on every switch, so it is the other
		// half of #1330: a switch-and-type sequence would resize the pty under
		// a line being entered. Someone typing needs no synthetic repaint.
		return nil
	}
	// Only main-screen TUIs (claude/codex, whether in an AI tab or an
	// orchestrator pane) need the WINCH repaint: they only repaint on a real
	// geometry change, so a tab switch leaves them blank until their next diff.
	// Plain shells and alt-screen apps reconstruct from the replayed buffer, so a
	// nudge would just cause a needless reflow. The frontend fires this on every
	// switch and lets this gate decide.
	if !needsWINCHRepaint(managed) {
		return nil
	}
	// No attach delay on a switch: the program is already attached (unlike the
	// attach-marker path, which must wait for dtach to reattach first).
	go a.nudgeAIRepaint(managed, cols, rows, 0, gen)
	return nil
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

// CloseEnvironmentSessions tears down every managed PTY bound to the
// (tenant, environment) pair and returns the closed serial IDs so the frontend
// can drop them in one round-trip. Backs the sidebar's "close env" affordance;
// it is desktop-only and does NOT touch the cloud context's AWS state (see
// StopCloudContext).
//
// Targets are collected under a.mu and closed after releasing it: session.Close
// can call back into bookkeeping that re-acquires a.mu, so closing under the
// lock would deadlock.
func (a *App) CloseEnvironmentSessions(selection uiSelection) ([]int, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	targets := a.collectAndMarkClosedForSelection(selection)
	// Close is a real teardown: stop the env's workspace-sync worker so a
	// closed env stops mirroring its worktree. Startup starts one sync worker
	// per configured env, so without this a closed env keeps an rsync-over-ssh
	// poller running; reopening the env restarts it.
	a.stopWorkspaceSyncForSelection(selection)
	// The env's pod observation describes sessions that no longer have a tab.
	// Dropping it means reopening the env starts from a fresh reading rather
	// than one taken before the close (or before a deploy replaced the pod).
	a.forgetSessionHeartbeats(selection)
	return closeManagedTerminals(targets)
}

// collectAndMarkClosedForSelection gathers the env's live sessions and marks
// each closed under a.mu BEFORE the caller shuts their PTYs down. Marking
// closed here is what makes "close env" a genuine teardown rather than a
// reconnect: tryReconnect and the spawn-reuse branch both refuse a closed
// session, so streamSession reaches finalizeSessionExit — which emits the
// terminal-exit and the AI-activity busy=false that clears the sidebar
// spinner — instead of reconnecting the still-live pod session and leaving the
// row spinning forever. Mirrors EndAISessions' mark-closed-under-lock.
func (a *App) collectAndMarkClosedForSelection(selection uiSelection) []*managedTerminal {
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
		managed.closed = true
		a.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	return targets
}

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
// current launch flags (--effort / --model / --verbose --debug). A launch
// flag only takes effect when the persistent session's
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
		launch := eruncommon.AISessionLaunchCommand(envConfig.AITool, envConfig.Claude, selection.Tenant, selection.Environment)
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

func decodePastedFilePayload(payload pastedFilePayload) ([]byte, string, error) {
	value := strings.TrimSpace(payload.Data)
	mimeType := strings.TrimSpace(payload.MIMEType)
	if strings.HasPrefix(value, "data:") {
		header, body, ok := strings.Cut(value, ",")
		if !ok {
			return nil, "", fmt.Errorf("pasted file data URL is malformed")
		}
		value = body
		if mimeType == "" {
			mediaType := strings.TrimPrefix(header, "data:")
			mediaType, _, _ = strings.Cut(mediaType, ";")
			mimeType = strings.TrimSpace(mediaType)
		}
	}
	if value == "" {
		return nil, "", fmt.Errorf("pasted file data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("decode pasted file: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("pasted file data is empty")
	}
	return data, mimeType, nil
}

// spawnStreamSession runs streamSession in its own goroutine, tracked on
// a.sessionWG so shutdown (and tests via t.Cleanup) can wait for every
// spawned reader to actually exit instead of just asking its session to
// close. Without that wait, a goroutine left mid-Read races whatever a
// caller's next teardown step touches — the failure mode that surfaced as a
// race on the adrg/xdg package's globals between a still-running session and
// a later t.Cleanup's xdg.Reload.
func (a *App) spawnStreamSession(managed *managedTerminal) {
	a.sessionWG.Add(1)
	go func() {
		defer a.sessionWG.Done()
		a.streamSession(managed)
	}()
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

func (a *App) handleSessionOutput(managed *managedTerminal, chunk []byte, lastOutputActivity *time.Time) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: managed.serial,
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
	a.feedActivityTraceFromTerminal(managed, chunk)
	a.recordAIActivity(managed)
	a.maybeNudgeAIRepaint(managed, chunk)
	if managed.clearIdleBlockOnOutput {
		a.mu.Lock()
		a.releaseIdleBlockLocked(managed)
		a.mu.Unlock()
	}
	if time.Since(*lastOutputActivity) >= 2*time.Second {
		// Reconnect noise must not refresh the idle marker; only real
		// interaction (which clears the flag in SendSessionInput) does.
		if !a.isAwaitingPostRespawnInput(managed) {
			a.recordTerminalActivity(managed.selection)
			*lastOutputActivity = time.Now()
		}
	}
}

// aiRepaintNudgeDelay waits after the attach marker so the bootstrap's
// `dtach -A` has reattached the client to Claude and Claude is listening for
// the WINCH; aiRepaintNudgeSettle spaces the shrink and restore so the two
// SIGWINCHes are not coalesced.
var (
	aiRepaintNudgeDelay  = 400 * time.Millisecond
	aiRepaintNudgeSettle = defaultAIRepaintNudgeSettle()
)

// aiRepaintInputQuiet is how recently input must have arrived for the repaint
// nudge to stand down. The nudge exists to repaint a BLANK reattached screen; a
// pane receiving keystrokes is by definition not that, and resizing it mid-line
// is what corrupted submitted prompts (#1330). Generous on purpose: skipping a
// repaint costs one keypress to fix, while a bad reflow costs the message.
const aiRepaintInputQuiet = 1500 * time.Millisecond

// noteSessionInput records that real user input just reached this pane.
func (a *App) noteSessionInput(managed *managedTerminal) {
	if managed == nil {
		return
	}
	a.mu.Lock()
	managed.inputGen++
	managed.lastInputAt = time.Now()
	a.mu.Unlock()
}

// sessionInputGen reads the pane's input counter so a nudge can detect input
// that arrived after it was scheduled.
func (a *App) sessionInputGen(managed *managedTerminal) uint64 {
	if managed == nil {
		return 0
	}
	a.mu.Lock()
	gen := managed.inputGen
	a.mu.Unlock()
	return gen
}

// typedRecentlyLocked reports whether the pane saw input inside the quiet
// window. Caller holds a.mu.
func typedRecentlyLocked(managed *managedTerminal) bool {
	return !managed.lastInputAt.IsZero() && time.Since(managed.lastInputAt) < aiRepaintInputQuiet
}

// defaultAIRepaintNudgeSettle sizes the shrink-hold to the platform's resize
// delivery. POSIX kubectl exec delivers the resize via SIGWINCH immediately, so
// a short hold suffices. On Windows there is no SIGWINCH: kubectl exec -it POLLS
// the terminal size (~250ms), so the shrink must be held past a poll interval or
// the pod TTY never sees it — and then Claude, a main-screen TUI that only
// repaints on a real geometry change, stays blank after a reattach until the
// user types (verified: a held ConPTY resize does reach the pod TTY; a 150ms
// blip does not).
func defaultAIRepaintNudgeSettle() time.Duration {
	if goruntime.GOOS == "windows" {
		return 600 * time.Millisecond
	}
	return 150 * time.Millisecond
}

// aiAttachMarker is the window-title escape (OSC 0) the open bootstrap prints
// immediately before `dtach -A` reattaches to the running program — the precise
// "about-to-reattach" signal. Nudging on the first output overall instead would
// fire too early, before the pod-side attach.
var aiAttachMarker = []byte("\x1b]0;")

func isAITabKind(managed *managedTerminal) bool {
	return managed != nil &&
		(managed.kind == sessionKindAI || managed.kind == sessionKindContributeAI)
}

// needsWINCHRepaint reports whether a session runs a main-screen TUI that only
// repaints on a real pty geometry change (a WINCH), so a same-size tab switch
// or reattach leaves it blank until one is forced. AI tabs and the orchestrator
// both run that kind of program (claude/codex). Kept separate from isAITabKind,
// which also drives AI-activity accounting (managedAITabFor, aiActivityKind)
// where an orchestrator is deliberately excluded for unrelated reasons — see
// aiActivityKind's comment.
func needsWINCHRepaint(managed *managedTerminal) bool {
	return isAITabKind(managed) || (managed != nil && managed.kind == sessionKindOrchestrator)
}

// maybeNudgeAIRepaint fires the AI repaint nudge when the attach marker first
// appears. dtach hands a reattached client a cleared screen, but Claude (an Ink
// main-screen TUI) only fully repaints on an actual geometry change — a
// same-size reattach raises no effective WINCH, so the tab would render blank.
// The nudge forces that geometry change once Claude is attached.
func (a *App) maybeNudgeAIRepaint(managed *managedTerminal, chunk []byte) {
	if !needsWINCHRepaint(managed) || !bytes.Contains(chunk, aiAttachMarker) {
		return
	}
	a.mu.Lock()
	if managed.repaintNudged || managed.closed {
		a.mu.Unlock()
		return
	}
	if typedRecentlyLocked(managed) {
		// The user is at the keyboard, so the pane is not the blank screen
		// this nudge repaints -- and their typing will force the redraw
		// anyway. Deliberately does NOT set repaintNudged: this attach has
		// not been nudged, so a later chunk may still do it once they stop.
		a.mu.Unlock()
		return
	}
	managed.repaintNudged = true
	cols, rows := managed.lastCols, managed.lastRows
	gen := managed.inputGen
	a.mu.Unlock()
	go a.nudgeAIRepaint(managed, cols, rows, aiRepaintNudgeDelay, gen)
}

// nudgeAIRepaint briefly shrinks the backend pty by one row and restores it:
// the change reaches Claude as a real WINCH and forces the full repaint a
// same-size reattach cannot. The local xterm is never resized, so the user sees
// the tab's content appear with no visible reflow.
func (a *App) nudgeAIRepaint(managed *managedTerminal, cols, rows int, initialDelay time.Duration, gen uint64) {
	if cols <= 0 || rows <= 1 {
		return
	}
	if initialDelay > 0 {
		time.Sleep(initialDelay)
	}
	// Input during the delay means the user started typing between the attach
	// marker and here. Shrink now and the restore reflows their line, so do
	// not start the cycle at all -- nothing has been resized yet, so bailing
	// costs nothing.
	if a.sessionInputGen(managed) != gen {
		return
	}
	if !a.resizeSessionIfLive(managed, cols, rows-1) {
		return
	}
	// Past this point the pty IS a row short, so it must be restored on every
	// path. awaitRepaintSettle returns early on input so the shrunken geometry
	// is held across as little typing as possible.
	a.awaitRepaintSettle(managed, gen)
	a.resizeSessionIfLive(managed, cols, rows)
}

// awaitRepaintSettle waits out the nudge settle, returning as soon as user
// input arrives so the caller can restore the pty immediately.
func (a *App) awaitRepaintSettle(managed *managedTerminal, gen uint64) {
	const slice = 10 * time.Millisecond
	for waited := time.Duration(0); waited < aiRepaintNudgeSettle; waited += slice {
		time.Sleep(slice)
		if a.sessionInputGen(managed) != gen {
			return
		}
	}
}

// resizeSessionIfLive guards the AI repaint nudge: a session that exits
// mid-nudge must not panic on a nil/closed pty.
func (a *App) resizeSessionIfLive(managed *managedTerminal, cols, rows int) bool {
	a.mu.Lock()
	session := managed.session
	closed := managed.closed
	a.mu.Unlock()
	if session == nil || closed {
		return false
	}
	_ = session.Resize(cols, rows)
	return true
}

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

// reconnectRefused decides whether tryReconnect should decline to respawn,
// emitting the matching terminal marker and env status as a side effect. Each
// guard is a terminal condition — handover, stopped cloud context, deploy
// failure, fast-exit loop — where an automatic respawn would fight another
// actor or storm a broken env; the recovery affordance is named in the marker.
func (a *App) reconnectRefused(managed *managedTerminal, exitReason string) bool {
	// An orchestrator has no env selection, so every guard below that keys on
	// managed.selection would silently pass it through. It gets its own check
	// instead: refuse a clean exit (the operator quit the TUI, not a crash)
	// and refuse whenever the operator's Stop already removed this
	// orchestrator's registration, so Stop refuses its own respawn.
	if managed.kind == sessionKindOrchestrator && a.orchestratorReconnectRefused(managed, exitReason) {
		return true
	}

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
	// empty state).
	if !a.shouldRespawnForCloudContext(managed) {
		a.emitStoppedContextMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusStopped)
		return true
	}

	// A runtime scaled to zero is a stopped environment, not a broken one.
	// It must be checked BEFORE the deploy-failure guards below, or the
	// tabs that die when the pod goes away flag the row as failed and the
	// operator's own Stop looks like an outage. Respawning is refused for
	// the same reason the cloud-context guard refuses: `erun open` wakes a
	// stopped env, so an automatic respawn would undo the stop the operator
	// just asked for. Re-clicking the row is the deliberate wake.
	if a.runtimeStoppedForSelection(managed.selection) {
		a.emitStoppedRuntimeMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusRuntimeStopped)
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
	// single explicit retry affordance.
	if a.trackExitForLoopGuard(managed) {
		a.emitReconnectLoopMarker(managed.serial)
		a.emitEnvStatus(managed.selection, envStatusFailed)
		return true
	}

	return false
}

// orchestratorReconnectRefused is reconnectRefused's orchestrator-specific
// guard. terminalSessionExitReason returns "" for a clean exit (Wait()
// reported no error) — the operator quitting the TUI from inside, not a
// failure — and a clean exit must never trigger the auto-resume this bound
// exists for. stopOrchestratorSession deletes both a.orchestrators[id] and
// a.sessions[key] under the same lock a Stop takes, so checking they are
// still exactly this registration is what makes Stop refuse its own respawn
// even if it raced tryReconnect's own managed.closed check.
func (a *App) orchestratorReconnectRefused(managed *managedTerminal, exitReason string) bool {
	if strings.TrimSpace(exitReason) == "" {
		return true
	}
	id, ok := orchestratorIDFromSessionKey(managed.key)
	if !ok {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.orchestrators[id] == nil || a.sessions[managed.key] != managed
}

func (a *App) tryReconnect(managed *managedTerminal, exitReason string) bool {
	a.mu.Lock()
	if managed == nil || managed.closed || managed.respawn == nil {
		a.mu.Unlock()
		return false
	}
	respawn := managed.respawn
	a.mu.Unlock()

	if a.reconnectRefused(managed, exitReason) {
		return false
	}

	a.emitReconnectMarker(managed.serial, exitReason)
	// The respawned `erun open` is a pure primitive; refresh the
	// shared thin reconnect (TTL-deduped) so a replaced pod's forwarders are
	// rebound without every tab repeating it. If the pod is gone
	// for good the reconnect surfaces a recoverable failure rather than
	// silently redeploying — deploy stays the caller's explicit action. An
	// orchestrator has no env runtime to rebind at all.
	if managed.kind != sessionKindLocal && managed.kind != sessionKindOrchestrator {
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
	// A reconnect is a fresh attach to the (possibly same) pod session, so
	// re-arm the AI repaint nudge: the new client gets a cleared screen and
	// Claude needs the geometry-change WINCH again to repaint.
	managed.repaintNudged = false
	a.mu.Unlock()
	// The respawn went through — whatever stopped/failed condition the row
	// was flagged with is being retried, so clear it (the refusal paths
	// above re-flag on the next failure). An orchestrator has no env row to
	// clear a status on.
	if managed.kind != sessionKindOrchestrator {
		a.emitEnvStatus(managed.selection, "")
	}
	return true
}

// shouldRespawnForCloudContext reports whether the managed PTY can be safely
// relaunched. Local envs (no linked cloud context) always may; a cloud env may
// only while its context is running or starting, because respawning against a
// stopped or stopping context would fight the desktop's auto-stop. A store read
// error is treated as "allow" so a transient failure does not permanently break
// reconnect.
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
		if strings.TrimSpace(m.selection.Tenant) != selection.Tenant ||
			strings.TrimSpace(m.selection.Environment) != selection.Environment {
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

// reconnectLoopWindow and reconnectLoopMaxExits tune the fast-exit loop guard:
// large enough to absorb a couple of legitimate transient blips (an `erun open`
// retrying past a momentary AWS describe-instances error) without leaving the
// user staring at stacked reconnect noise.
const (
	reconnectLoopWindow     = 30 * time.Second
	reconnectLoopMaxExits   = 2
	reconnectLoopMarkerANSI = "\r\n\x1b[2;33m── stopped reconnecting after repeated failures — click the environment in the sidebar to retry ──\x1b[0m\r\n"
	deployFailedMarkerANSI  = "\r\n\x1b[2;33m── deploy failed — not retrying automatically; use Run doctor or Rebuild & redeploy on the failed deploy, or click the environment in the sidebar to retry ──\x1b[0m\r\n"
	takenOverMarkerANSI     = "\r\n\x1b[2;33m── session re-attached in another ERun window — click the environment in the sidebar to attach it here ──\x1b[0m\r\n"
)

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

// emitReconnectLoopMarker's dim-yellow style matches the reconnecting and
// stopped-context markers so the user reads them all as one status channel.
func (a *App) emitReconnectLoopMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(reconnectLoopMarkerANSI)),
	})
}

func (a *App) emitDeployFailedMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(deployFailedMarkerANSI)),
	})
}

func (a *App) emitTakenOverMarker(sessionID int) {
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(takenOverMarkerANSI)),
	})
}

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

// reconnectBlockedByDeployFailure reports whether this open ended in a deploy
// failure (signalReady got the `==> Deploy failed` readyErr) rather than a
// healthy ready that later dropped — the latter still reconnects.
func (a *App) reconnectBlockedByDeployFailure(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	managed.readyMu.Lock()
	defer managed.readyMu.Unlock()
	return managed.readyClosed && managed.readyErr != nil
}

// reconnectBlockedByActivityDeployFailure reports whether the env's latest
// deploy failed in a separate activity — the case reconnectBlockedByDeployFailure
// misses because this open has no deploy-failed readyErr of its own.
func (a *App) reconnectBlockedByActivityDeployFailure(managed *managedTerminal) bool {
	if managed == nil || a.activityQueue == nil {
		return false
	}
	return a.activityQueue.latestDeployFailed(managed.selection.Tenant, managed.selection.Environment)
}

// emitStoppedContextMarker's dim-yellow style matches the reconnecting marker
// so the user reads them as one status channel; naming the recovery (titlebar
// Play button) in the line itself means no separate UI element is needed.
func (a *App) emitStoppedContextMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click the start button in the titlebar to resume ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// emitStoppedRuntimeMarker names the other recovery: a runtime scaled to zero
// is woken by opening the environment, not by the titlebar's cloud-context
// start button.
func (a *App) emitStoppedRuntimeMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── environment stopped — click it in the sidebar to start it again ──\x1b[0m\r\n"
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
//
// aiActivityKind reports whether a session of this kind should drive the
// sidebar's working spinner. An orchestrator runs the same agent an AI tab
// does, and it is the row most likely to be working, so gating this on
// sessionKindAI alone left the one row that is always driving work as the only
// row that never showed it.
//
// It takes the kind rather than the session so each caller keeps its own
// `managed == nil` guard visible: folding the nil check in here hid it from
// static analysis, which then read every later field access as a possible nil
// dereference.
//
// An orchestrator is deliberately NOT here. It runs an interactive agent TUI,
// which repaints its prompt and counters continuously, so "this terminal is
// emitting bytes" is true forever and the silence rule that releases the latch
// never fires — the row span forever and the desktop burned CPU reading redraws
// to keep it that way. An env's AI tab survives the same weakness only because
// the pod heartbeat independently observes its program exit. An orchestrator has
// no pod, so it reports its own turn boundaries instead (orchestrator_activity.go).
func aiActivityKind(kind sessionKind) bool {
	return kind == sessionKindAI
}

func (a *App) recordAIActivity(managed *managedTerminal) {
	if managed == nil || !aiActivityKind(managed.kind) {
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

// clearAIActivityIfQuiet fires from recordAIActivity's AfterFunc and clears the
// busy latch only if the session has stayed quiet. If newer output arrived, a
// later recordAIActivity already reset the timer, so this firing is stale and a
// no-op.
//
// Silence alone is not enough to declare the work finished: an agent waiting on
// a compile, and a session whose output stream dropped, are both silent while
// the program in the pod keeps running. So a session the pod recently observed
// as running keeps the latch — the heartbeat poller releases it when the pod
// agrees it has stopped (applySessionHeartbeat).
func (a *App) clearAIActivityIfQuiet(managed *managedTerminal) {
	if managed == nil {
		return
	}
	if a.heartbeatSaysRunning(managed) {
		return
	}
	a.releaseAIActivityIfQuiet(managed)
}

func (a *App) releaseAIActivityIfQuiet(managed *managedTerminal) {
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

// releaseAIActivity drops the busy latch regardless of how recently the session
// printed. Used when the pod itself reports the session's program is gone: the
// stream may still be dribbling reconnect noise, but the work is over.
func (a *App) releaseAIActivity(managed *managedTerminal) {
	a.mu.Lock()
	if managed.closed || !managed.aiBusyEmitted {
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
	if managed == nil || !aiActivityKind(managed.kind) {
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

// emitEnvStatus publishes the env's real condition for the sidebar row.
// Status "" clears; envStatusStopped / envStatusFailed flag
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

// closeManagedLocked marks managed closed and hands back its underlying
// session for the caller to tear down. The `closed` field (and `session`,
// which callers read alongside it) is read everywhere else in this file under
// a.mu — currentSessionFor, tryReconnect, finalizeSessionExit, and more all
// take the lock before touching either. This is the one place that mutates
// them, so it must take the same lock rather than grow a second one; every
// caller here already holds a.mu.
func (a *App) closeManagedLocked(managed *managedTerminal) terminalSession {
	if managed == nil || managed.session == nil {
		return nil
	}
	managed.closed = true
	return managed.session
}

// closeManaged is closeManagedLocked for callers that do not already hold
// a.mu. The session's real teardown (session.Close(), a file/process
// operation) happens outside the lock, matching every other place in this
// file that only holds a.mu around the field mutation and not around the
// blocking I/O that follows it.
func (a *App) closeManaged(managed *managedTerminal) error {
	a.mu.Lock()
	session := a.closeManagedLocked(managed)
	a.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
}

func (a *App) closeAllSessionsLocked() {
	for _, managed := range a.sessions {
		if session := a.closeManagedLocked(managed); session != nil {
			_ = session.Close()
		}
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
	for key, managed := range a.sessions {
		if managed == nil {
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
		if session := a.closeManagedLocked(managed); session != nil {
			_ = session.Close()
		}
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

	// lastCols/lastRows track the most recent pty size: the AI-tab repaint
	// nudge needs a size to resize to, and the session does not expose its own
	// geometry.
	lastCols int
	lastRows int

	// repaintNudged guards the once-per-attach AI repaint nudge so it fires
	// on the first output after a (re)attach and not on every chunk.
	// tryReconnect clears it so the next attach nudges again.
	repaintNudged bool

	// lastInputAt/inputGen record real user input into this pane. The repaint
	// nudge changes pty geometry behind the user's back, so it must not fire
	// into a pane someone is typing into and must abandon a hold in progress
	// the moment they start: holding the pty a row short across a keystroke
	// reflowed the line being edited and corrupted the submitted prompt
	// (#1330). inputGen is a counter rather than a flag so a nudge can tell
	// "input since I was scheduled" from "input at some point".
	lastInputAt time.Time
	inputGen    uint64

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
	// exit never trips the cap.
	recentExits []time.Time

	// takenOver is set when the session's output carries the CLI's
	// taken-over notice: another ERun window re-attached this persistent
	// pod session (screen-style detach-and-reattach). tryReconnect must
	// then refuse to respawn — respawning would steal the session straight
	// back and the two windows would fight. Clicking the env in the
	// sidebar starts a fresh session, which is the deliberate take-back.
	takenOver bool

	// appSession is the persistent pod session id this terminal attaches to
	// (`ai`, `open-2`, `contribute-ai`, …), empty for sessions with no pod
	// session. The heartbeat poller keys its observations on it, so what the pod
	// reports is matched to the tab by identity rather than re-derived.
	appSession string

	// aiActiveSince / aiLastOutput / aiBusyEmitted / aiInactivityTimer
	// drive the debounced AI activity signal that powers the sidebar
	// "Claude is working" spinner. Only populated for sessionKindAI
	// managed terminals. See recordAIActivity for the debounce policy
	// (5 s sustained output to flip on, 3 s silence to flip off) and
	// session_heartbeat.go for the observed-liveness override that keeps a
	// quiet-but-running session from reading as finished.
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

// waitReady blocks until the session reaches ready, its process exits, ctx is
// cancelled, or timeout elapses (0 means no timeout).
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

// signalReady marks the session ready exactly once; later calls are no-ops
// because the first observed terminal-state line is authoritative.
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
	sessionKindOpen         sessionKind = "erun"
	sessionKindLocal        sessionKind = "local"
	sessionKindAI           sessionKind = "ai"
	sessionKindCommand      sessionKind = "command"
	sessionKindOrchestrator sessionKind = "orchestrator"
)

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

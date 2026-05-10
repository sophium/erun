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
	"github.com/wailsapp/wails/v2/pkg/runtime"
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

// runOpenSession is the original spawn logic for the ERun tab,
// wrapped so the desktop action runner can call it on its turn.
// Returns the result the Wails caller wants and the managedTerminal so
// the runner can wait on its ready signal.
func (a *App) runOpenSession(ctx context.Context, selection uiSelection, slot, cols, rows int) (startSessionResult, *managedTerminal, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

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

	openParams := startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildOpenArgs(result.Tenant, result.Environment, selection.Debug),
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
	a.busyEnvs[environmentBusyKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	go a.streamSession(managed)
	go a.startWorkspaceSyncForSelection(selection)

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
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

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
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

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

	tool := resolveAIToolCommand(result.EnvConfig.AITool)
	params := startTerminalSessionParams{
		Dir:          resolveTerminalStartDir(result.RepoPath),
		Executable:   a.deps.resolveCLIPath(),
		Args:         buildOpenArgs(result.Tenant, result.Environment, selection.Debug),
		Env:          []string{appSessionEnvVar + "=1"},
		Cols:         cols,
		Rows:         rows,
		InitialInput: []byte(tool + "\n"),
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

	a.logSpawnedCommandToLocal(selection, "ai", formatLocalCommandLog(formatLaunchCommand(params)+" && "+shellQuoteIfNeeded(tool), "AI tab"))
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
	args := append(buildDeployArgs(selection), "--force")
	return a.runErunCommandInLocal(selection, cols, rows, args)
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
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '=' || r == '+' || r == ':' || r == '@' || r == ',':
		default:
			return shellQuote(value)
		}
	}
	return value
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
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
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
			return err
		}
		params = localParams
		params.Env = []string{appSessionEnvVar + "=1"}
	} else if !result.EnvConfig.SSHD.Enabled {
		return fmt.Errorf("open %s requires sshd-enabled remote environment; run `erun sshd init %s %s` first", ide, selection.Tenant, selection.Environment)
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := a.deps.runIDECommand(ctx, params)
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(output); detail != "" {
		return fmt.Errorf("open %s: %w: %s", ide, err, detail)
	}
	return fmt.Errorf("open %s: %w", ide, err)
}

func (a *App) StartCloudInitAWSSession(cols, rows int) (startSessionResult, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}
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

func (a *App) startCommandSession(selection uiSelection, cols, rows int, key string, args []string, dir string, env []string) (startSessionResult, error) {
	return a.startCommandSessionWithExecutable(selection, cols, rows, key, a.deps.resolveCLIPath(), args, dir, env)
}

func (a *App) startCommandSessionWithExecutable(selection uiSelection, cols, rows int, key string, executable string, args []string, dir string, env []string) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

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
		Dir:        dir,
		Executable: executable,
		Args:       args,
		Env:        env,
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
		session:        session,
		selection:      selection,
		key:            key,
		serial:         serial,
		kind:           sessionKindCommand,
		blocksIdleStop: true,
		startedAt:      time.Now(),
	}
	a.sessions[key] = managed
	a.busyEnvs[environmentBusyKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	a.rememberKubeContextForActivity(selection.KubernetesContext)
	go a.streamSession(managed)

	return startSessionResult{
		SessionID: serial,
		Selection: selection,
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
	a.recordTerminalActivity(managed.selection)
	return nil
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
		if a.ctx == nil {
			return
		}
		runtime.EventsEmit(a.ctx, mcpReconnectLineEvent, line)
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
	a.mu.Unlock()
	if managed == nil || managed.session == nil {
		return nil
	}
	return managed.session.Close()
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
			payload := terminalOutputPayload{
				SessionID: managed.serial,
				Data:      base64.StdEncoding.EncodeToString(buffer[:count]),
			}
			a.emitEvent(terminalOutputEvent, payload)
			a.feedActivityTraceFromTerminal(managed, buffer[:count])
			if managed.clearIdleBlockOnOutput {
				a.mu.Lock()
				a.releaseIdleBlockLocked(managed)
				a.mu.Unlock()
			}
			if time.Since(lastOutputActivity) >= 2*time.Second {
				a.recordTerminalActivity(managed.selection)
				lastOutputActivity = time.Now()
			}
		}
		if err == nil {
			continue
		}
		reason := terminalSessionExitReason(current, err)
		if a.tryReconnect(managed, reason) {
			continue
		}
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
		return
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

func (a *App) tryReconnect(managed *managedTerminal, exitReason string) bool {
	a.mu.Lock()
	if managed == nil || managed.closed || managed.respawn == nil {
		a.mu.Unlock()
		return false
	}
	respawn := managed.respawn
	a.mu.Unlock()

	a.emitReconnectMarker(managed.serial, exitReason)
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
	a.mu.Unlock()
	return true
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
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, payload)
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

func resolveInitStartDir(findProjectRoot eruncommon.ProjectFinderFunc) string {
	if findProjectRoot != nil {
		if _, projectRoot, err := findProjectRoot(); err == nil && strings.TrimSpace(projectRoot) != "" {
			return resolveTerminalStartDir(projectRoot)
		}
	}
	return resolveTerminalStartDir("")
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
	return selection.Tenant + "\x00" + selection.Environment + "\x00" + fmt.Sprintf("%t", selection.Debug)
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

func environmentBusyKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return selection.Tenant + "\x00" + selection.Environment
}

func (a *App) releaseIdleBlockLocked(managed *managedTerminal) {
	if managed == nil || !managed.blocksIdleStop {
		return
	}
	busyKey := environmentBusyKey(managed.selection)
	if a.busyEnvs[busyKey] <= 1 {
		delete(a.busyEnvs, busyKey)
	} else {
		a.busyEnvs[busyKey]--
	}
	managed.blocksIdleStop = false
	managed.clearIdleBlockOnOutput = false
}

func initSelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + selection.Version + "\x00" + selection.RuntimeImage + "\x00" + selection.RuntimeCPU + "\x00" + selection.RuntimeMemory + "\x00" + selection.KubernetesContext + "\x00" + selection.ContainerRegistry + "\x00" + fmt.Sprintf("%t", selection.SetDefaultTenant) + "\x00" + fmt.Sprintf("%t", selection.NoGit) + "\x00" + fmt.Sprintf("%t", selection.Bootstrap) + "\x00" + fmt.Sprintf("%t", selection.Debug)
}

func deploySelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + selection.Version + "\x00" + selection.RuntimeImage + "\x00" + fmt.Sprintf("%t", selection.Debug)
}

func sshdInitSelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "sshd-init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + fmt.Sprintf("%t", selection.Debug)
}

func doctorSelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "doctor\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + fmt.Sprintf("%t", selection.Debug)
}

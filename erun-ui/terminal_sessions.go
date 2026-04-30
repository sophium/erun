package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) StartSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
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

	key := selectionKey(selection)
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.current = existing
		a.mu.Unlock()
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
		}, nil
	}
	a.mu.Unlock()

	session, err := a.deps.startTerminal(startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildOpenArgs(result.Tenant, result.Environment, selection.Debug),
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
		session:                session,
		selection:              selection,
		key:                    key,
		serial:                 serial,
		blocksIdleStop:         true,
		clearIdleBlockOnOutput: true,
	}
	a.sessions[key] = managed
	a.current = managed
	a.busyEnvs[environmentBusyKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	go a.streamSession(managed)

	return startSessionResult{
		SessionID: serial,
		Selection: selection,
	}, nil
}

func (a *App) StartInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.startCommandSession(selection, cols, rows, initSelectionKey(selection), buildInitArgs(selection), resolveInitStartDir(a.deps.findProjectRoot), []string{appSessionEnvVar + "=1"})
}

func (a *App) StartDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}
	return a.startCommandSession(selection, cols, rows, deploySelectionKey(selection), buildDeployArgs(selection), resolveDeployStartDir(a.deps.findProjectRoot, result), []string{appSessionEnvVar + "=1"})
}

func (a *App) StartSSHDInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}
	return a.startCommandSession(selection, cols, rows, sshdInitSelectionKey(selection), buildSSHDInitArgs(selection), resolveDeployStartDir(a.deps.findProjectRoot, result), []string{appSessionEnvVar + "=1"})
}

func (a *App) StartDoctorSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}
	return a.startCommandSession(selection, cols, rows, doctorSelectionKey(selection), buildDoctorArgs(selection), resolveDeployStartDir(a.deps.findProjectRoot, result), []string{appSessionEnvVar + "=1"})
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

	cliPath := a.deps.resolveCLIPath()
	executable := cliPath
	args := buildOpenIDEArgs(selection, ide)
	if !result.EnvConfig.SSHD.Enabled {
		return fmt.Errorf("open %s requires sshd-enabled remote environment; run `erun sshd init %s %s` first", ide, selection.Tenant, selection.Environment)
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	output, err := a.deps.runIDECommand(ctx, startTerminalSessionParams{
		Dir:        resolveDeployStartDir(a.deps.findProjectRoot, result),
		Executable: executable,
		Args:       args,
		Env:        []string{appSessionEnvVar + "=1"},
	})
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
		a.current = existing
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
		session: session,
		key:     key,
		serial:  serial,
	}
	a.sessions[key] = managed
	a.current = managed
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
		a.current = existing
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
		blocksIdleStop: true,
	}
	a.sessions[key] = managed
	a.current = managed
	a.busyEnvs[environmentBusyKey(selection)]++
	a.mu.Unlock()

	a.recordTerminalActivity(selection)
	go a.streamSession(managed)

	return startSessionResult{
		SessionID: serial,
		Selection: selection,
	}, nil
}

func (a *App) SendSessionInput(data string) error {
	if data == "" {
		return nil
	}

	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current == nil || current.session == nil {
		return nil
	}

	if _, err := io.WriteString(current.session, data); err != nil {
		return err
	}
	a.recordTerminalActivity(current.selection)
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

func (a *App) SavePastedImage(payload pastedImagePayload) (pastedImageResult, error) {
	data, mimeType, err := decodePastedImagePayload(payload)
	if err != nil {
		return pastedImageResult{}, err
	}

	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current == nil || current.session == nil {
		return pastedImageResult{}, fmt.Errorf("no active terminal session")
	}

	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      current.selection.Tenant,
		Environment: current.selection.Environment,
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

func (a *App) LoadDiff(selection uiSelection) (eruncommon.DiffResult, error) {
	selection = normalizeSelection(selection)
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
	if err := a.ensureMCPAvailable(ctx, result); err != nil {
		return eruncommon.DiffResult{}, err
	}
	endpoint := mcpEndpointForOpenResult(result)
	diff, err := a.deps.loadDiff(ctx, endpoint)
	if err == nil || a.deps.ensureMCP == nil {
		return diff, err
	}
	if ensureErr := a.deps.ensureMCP(ctx, result); ensureErr != nil {
		return eruncommon.DiffResult{}, err
	}
	return a.deps.loadDiff(ctx, endpoint)
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

func (a *App) ResizeSession(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}

	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current == nil || current.session == nil {
		return nil
	}

	return current.session.Resize(cols, rows)
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
		count, err := managed.session.Read(buffer)
		if count > 0 {
			payload := terminalOutputPayload{
				SessionID: managed.serial,
				Data:      base64.StdEncoding.EncodeToString(buffer[:count]),
			}
			a.emitEvent(terminalOutputEvent, payload)
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
		if err != nil {
			reason := terminalSessionExitReason(managed.session, err)
			a.mu.Lock()
			managed.closed = true
			if existing := a.sessions[managed.key]; existing == managed {
				delete(a.sessions, managed.key)
			}
			a.releaseIdleBlockLocked(managed)
			if a.current == managed {
				a.current = nil
			}
			a.mu.Unlock()
			a.emitEvent(terminalExitEvent, terminalExitPayload{
				SessionID: managed.serial,
				Reason:    reason,
			})
			return
		}
	}
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
	closed := make(map[*managedTerminal]struct{}, len(a.sessions))
	for _, session := range a.sessions {
		if session == nil {
			continue
		}
		if _, seen := closed[session]; seen {
			continue
		}
		closed[session] = struct{}{}
		_ = session.Close()
	}
	if a.current != nil {
		if _, seen := closed[a.current]; !seen {
			_ = a.current.Close()
		}
	}
	a.sessions = make(map[string]*managedTerminal)
	a.current = nil
}

func (a *App) closeSessionsForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	prefixes := []string{
		selectionKey(selection),
		"init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, session := range a.sessions {
		if session == nil {
			continue
		}
		matches := false
		for _, prefix := range prefixes {
			if key == prefix || strings.HasPrefix(key, prefix) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		_ = session.Close()
		delete(a.sessions, key)
		if a.current == session {
			a.current = nil
		}
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
	closed                 bool
	blocksIdleStop         bool
	clearIdleBlockOnOutput bool
}

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

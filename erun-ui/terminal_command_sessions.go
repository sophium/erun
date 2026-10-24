package main

import (
	"encoding/base64"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

func (a *App) StartInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	// Init owns the env's single deploy, so it carries the MCP-auth key; a later
	// redeploy would roll the new pod.
	return a.runErunCommandInLocal(selection, cols, rows, a.appendMCPAuthPublicKeyFlag(buildInitArgs(selection)))
}

// StartDeploySession installs a published version and never builds; with no
// picked version it redeploys the current one.
func (a *App) StartDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.runErunCommandInLocal(selection, cols, rows, a.appendMCPAuthPublicKeyFlag(buildDeployArgs(selection)))
}

// StartCreateVersionSession builds, pushes and deploys the working tree. Only a
// local-agent env qualifies; ResolvedType is checked because a host env also
// builds here but has no pod.
func (a *App) StartCreateVersionSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("start create-version session", selection.Tenant, selection.Environment); err != nil {
		return startSessionResult{}, err
	}
	if a.ctx == nil {
		return startSessionResult{}, fmt.Errorf("start create-version session: application context is not ready")
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}
	if result.EnvConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		return startSessionResult{}, fmt.Errorf("%s/%s is a host environment; it has no pod and no cluster to deploy to", selection.Tenant, selection.Environment)
	}
	if !result.EnvConfig.BuildsHere() || result.RemoteRepo() {
		return startSessionResult{}, fmt.Errorf("%s/%s has no local source to build; deploy a published version instead", selection.Tenant, selection.Environment)
	}
	go func() {
		_ = a.runDeployOrchestration(a.activityWatcherCtx(), selection, result, false)
	}()
	return startSessionResult{Selection: normalizeSelection(selection), Orchestrated: true}, nil
}

// StartInitialDeploySession deploys a freshly created env's first version,
// building only when the env builds here.
func (a *App) StartInitialDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	if result, ok := a.maybeStartDeployOrchestration(selection, false); ok {
		return result, nil
	}
	return a.runErunCommandInLocal(selection, cols, rows, a.appendMCPAuthPublicKeyFlag(buildDeployArgs(selection)))
}

// StartForceDeploySession runs `erun deploy --force` to recover a missing image.
func (a *App) StartForceDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	if result, ok := a.maybeStartDeployOrchestration(selection, true); ok {
		return result, nil
	}
	args := a.appendMCPAuthPublicKeyFlag(append(buildDeployArgs(selection), "--force"))
	return a.runErunCommandInLocal(selection, cols, rows, args)
}

// StartUpgradeEnvironmentSession runs `erun upgrade` in the env's own Local shell.
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
	if err := errMissingTenantOrEnvironment("run erun command in local session", selection.Tenant, selection.Environment); err != nil {
		return startSessionResult{}, err
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
		// PowerShell needs the call operator to run a quoted path, and a CR to submit:
		// PSReadLine treats LF as a continuation.
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

// powerShellQuoteIfNeeded single-quotes value only when it is unsafe bare.
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

// StartCloudInitCloudflareSession runs `erun cloud init cloudflare`; the CLI owns
// the flow.
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

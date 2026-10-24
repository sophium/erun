package main

import (
	"encoding/base64"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

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
// deploys it (build -> push -> deploy). Only a local-agent env builds and
// deploys through this local orchestration; a runtime/consumer env produces
// nothing, so this errors and the operator deploys a published version
// instead, and a host env has no pod to deploy to at all regardless of it
// having local source to build. The env-create flow's first deploy runs
// through here too. Checked against ResolvedType rather than the broader
// "BuildsHere() && !RemoteRepo()" combination, which a host env also
// satisfies.
func (a *App) StartCreateVersionSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("start create-version session", selection.Tenant, selection.Environment); err != nil {
		return startSessionResult{}, err
	}
	if a.ctx == nil {
		return startSessionResult{}, fmt.Errorf("start create-version session: application context is not ready")
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
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

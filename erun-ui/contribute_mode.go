package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// uiContributeState is the JSON-facing read model the frontend uses to
// drive the per-env Contribute toggle.
type uiContributeState struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Enabled     bool   `json:"enabled"`
}

// uiContributeAppLaunch carries the URL the frontend should open in a
// browser tab so the user can use their locally-built ERun desktop app.
type uiContributeAppLaunch struct {
	URL  string `json:"url"`
	Port int    `json:"port"`
}

const contributeChangedEvent = "contribute:changed"

// reservedContributeTenant is the tenant name that owns the ERun project
// itself. Contribute mode lets users contribute back TO that project
// from any other env; offering it inside the erun tenant would be
// self-referential and confusing, so the toggle is hidden there.
const reservedContributeTenant = "erun"

// IsContributeEligible reports whether the toggle is allowed for the
// given env. Exported through Wails so the frontend can hide the control
// for envs where contribute mode would not make sense.
func (a *App) IsContributeEligible(selection uiSelection) bool {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return false
	}
	if strings.EqualFold(selection.Tenant, reservedContributeTenant) {
		return false
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return false
	}
	switch eruncommon.EnvironmentType(envConfig.Type) {
	case eruncommon.EnvironmentTypeLocalAgent, eruncommon.EnvironmentTypeRemoteAgent:
		return true
	default:
		return false
	}
}

// GetContributeMode returns whether the env is currently flagged in
// contribute mode. Returns false for envs that don't have a stored flag.
func (a *App) GetContributeMode(selection uiSelection) bool {
	if a.contribute == nil {
		return false
	}
	return a.contribute.get(selection)
}

// SetContributeMode toggles contribute mode for the env. When turning
// ON, validates eligibility and ensures the ERun clone exists inside
// the env via the contribute_clone MCP tool. Persists the flag to the
// UI state file and fires a contribute:changed event so the frontend
// can update tab strip + diff source accordingly.
//
// Caller (the frontend thunk) is responsible for spawning or closing
// the two contribute tabs after this call returns.
func (a *App) SetContributeMode(selection uiSelection, on bool) (uiContributeState, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiContributeState{}, fmt.Errorf("tenant and environment are required")
	}
	if on {
		if !a.IsContributeEligible(selection) {
			return uiContributeState{}, fmt.Errorf("contribute mode is only available for non-erun local-agent and remote-agent environments")
		}
		if err := a.runEnsureErunClone(selection); err != nil {
			return uiContributeState{}, err
		}
	} else {
		a.closeContributeSessionsForSelection(selection)
		a.stopContributeAppForward(selection)
	}
	if a.contribute != nil {
		a.contribute.set(selection, on)
		if err := a.contribute.saveToDisk(); err != nil {
			return uiContributeState{}, fmt.Errorf("persist contribute state: %w", err)
		}
	}
	state := uiContributeState{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Enabled:     on,
	}
	a.emit(contributeChangedEvent, state)
	return state, nil
}

func (a *App) runEnsureErunClone(selection uiSelection) error {
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return fmt.Errorf("resolve environment: %w", err)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.canConnectLocalPort != nil && !a.deps.canConnectLocalPort(mcpPort) {
		return wrapMCPUnreachableError(fmt.Errorf("mcp port %d is not reachable; open the environment first", mcpPort))
	}
	endpoint := mcpEndpointForOpenResult(result)
	if a.deps.cloneERun == nil {
		return fmt.Errorf("contribute clone is not configured")
	}
	if err := a.deps.cloneERun(ctx, endpoint); err != nil {
		if isMCPDialFailure(err) {
			return wrapMCPUnreachableError(err)
		}
		return fmt.Errorf("clone ERun: %w", err)
	}
	return nil
}

// StartContributeApp is the Wails-exposed entrypoint for the "Open
// contribute app" affordance. It boots `erun app --headless --port N`
// inside the env's ERun (contribute) tab, brings up a kubectl
// port-forward for the contribute-app port, waits for the headless
// server to accept connections, and returns the http URL the frontend
// should open in the user's browser.
// resolveContributeAppPort validates that the selection is in contribute mode
// and resolves the env's allocated contribute-app port. It returns an error
// describing the first failing precondition (missing tenant/env, contribute
// mode off, environment resolution failure, or an unallocated port).
func (a *App) resolveContributeAppPort(selection uiSelection) (int, error) {
	if selection.Tenant == "" || selection.Environment == "" {
		return 0, fmt.Errorf("tenant and environment are required")
	}
	if !a.GetContributeMode(selection) {
		return 0, fmt.Errorf("contribute mode is not enabled for %s/%s", selection.Tenant, selection.Environment)
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return 0, fmt.Errorf("resolve environment: %w", err)
	}
	port := eruncommon.ContributeAppPortForResult(result)
	if port <= 0 {
		return 0, fmt.Errorf("contribute-app port is not allocated for %s/%s", selection.Tenant, selection.Environment)
	}
	return port, nil
}

func (a *App) StartContributeApp(selection uiSelection) (uiContributeAppLaunch, error) {
	selection = normalizeSelection(selection)
	port, err := a.resolveContributeAppPort(selection)
	if err != nil {
		return uiContributeAppLaunch{}, err
	}

	// Send the headless start command into the contribute ERun tab so
	// the user can see the build progress and so the running process
	// is visible (and Ctrl-Cable) in the contribute terminal they
	// already have open. Best-effort: if the tab is missing we still
	// try the port-forward so a manually-launched headless on the same
	// port still becomes reachable from host.
	a.sendCommandToContributeERunSession(selection, fmt.Sprintf("erun app --headless --port %d\n", port))

	if a.contributeApps == nil {
		a.contributeApps = newContributeAppForwards()
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	forward, args, localPort, err := a.startContributeAppForward(ctx, selection)
	if err != nil {
		return uiContributeAppLaunch{}, err
	}
	if forward != nil {
		a.contributeApps.put(selection, forward)
	}
	if err := waitForContributeAppReachable(ctx, localPort, forward, args); err != nil {
		a.stopContributeAppForward(selection)
		return uiContributeAppLaunch{}, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", localPort)
	// Open the URL in the user's *default* browser via the Wails runtime.
	// window.open from the React side runs inside the WKWebView and
	// does not escape to an external browser, so the launcher button
	// felt broken until the user manually copy-pasted the URL.
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, url)
	}
	return uiContributeAppLaunch{
		URL:  url,
		Port: localPort,
	}, nil
}

// stopContributeAppForward tears down the kubectl port-forward and
// best-effort sends Ctrl-C to the contribute ERun tab so the headless
// process exits too. Safe to call when nothing is running.
func (a *App) stopContributeAppForward(selection uiSelection) {
	selection = normalizeSelection(selection)
	if a.contributeApps != nil {
		if forward := a.contributeApps.take(selection); forward != nil {
			forward.stop()
		}
	}
	// 0x03 = Ctrl-C. The contribute ERun tab is a normal interactive
	// shell, so writing the ETX byte interrupts the foreground
	// process (the headless erun app, if it's still running) without
	// killing the shell. If the tab is gone or the shell is at a
	// prompt this is a no-op.
	a.sendCommandToContributeERunSession(selection, "\x03")
}

// sendCommandToContributeERunSession looks up the contribute ERun PTY
// for the env and writes the given bytes to its stdin. Best-effort: if
// the tab does not exist (toggle was off, session was closed, etc.) the
// call returns without error.
func (a *App) sendCommandToContributeERunSession(selection uiSelection, command string) {
	prefix := "contribute-erun\x00" + selectionKey(selection) + "\x00"
	a.mu.Lock()
	var target *managedTerminal
	for key, managed := range a.sessions {
		if managed == nil || managed.closed || managed.session == nil {
			continue
		}
		if strings.HasPrefix(key, prefix) {
			target = managed
			break
		}
	}
	a.mu.Unlock()
	if target == nil {
		return
	}
	_, _ = io.WriteString(target.session, command)
}

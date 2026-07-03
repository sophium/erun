package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type uiContributeState struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Enabled     bool   `json:"enabled"`
}

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

// IsContributeEligible reports whether contribute mode is allowed for the given env.
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

// GetContributeMode reports whether the env is currently flagged in contribute mode.
func (a *App) GetContributeMode(selection uiSelection) bool {
	if a.contribute == nil {
		return false
	}
	return a.contribute.get(selection)
}

// SetContributeMode toggles contribute mode for the env; enabling it
// requires an eligible env and clones ERun into it first.
//
// The caller (the frontend thunk) spawns or closes the contribute tabs
// after this returns; this method does not touch them.
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
	if err := a.deps.cloneERun(ctx, endpoint, a.mcpBearer(result.Tenant, result.EnvConfig.Name)); err != nil {
		if isMCPDialFailure(err) {
			return wrapMCPUnreachableError(err)
		}
		return fmt.Errorf("clone ERun: %w", err)
	}
	return nil
}

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

// StartContributeApp backs the "Open contribute app" affordance: it runs
// the headless ERun app in the env's contribute tab and returns the URL
// the frontend opens in the browser.
func (a *App) StartContributeApp(selection uiSelection) (uiContributeAppLaunch, error) {
	selection = normalizeSelection(selection)
	port, err := a.resolveContributeAppPort(selection)
	if err != nil {
		return uiContributeAppLaunch{}, err
	}

	// Run the headless server inside the contribute tab (not silently) so
	// the user sees build progress and can Ctrl-C it in the terminal they
	// already have open. Best-effort: if that tab is gone, the port-forward
	// below still reaches a manually-launched headless on the same port.
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
	// window.open from the React side stays inside the WKWebView and never
	// reaches an external browser, so open the URL through the Wails runtime
	// instead — otherwise the launcher button feels broken.
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, url)
	}
	return uiContributeAppLaunch{
		URL:  url,
		Port: localPort,
	}, nil
}

func (a *App) stopContributeAppForward(selection uiSelection) {
	selection = normalizeSelection(selection)
	if a.contributeApps != nil {
		if forward := a.contributeApps.take(selection); forward != nil {
			forward.stop()
		}
	}
	// 0x03 (Ctrl-C) interrupts the foreground headless process in the
	// interactive contribute shell without killing the shell itself.
	a.sendCommandToContributeERunSession(selection, "\x03")
}

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

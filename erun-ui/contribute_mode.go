package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
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

// reservedContributeTenant owns ERun itself; contributing from it would be self-referential.
const reservedContributeTenant = "erun"

func (a *App) IsContributeEligible(selection uimodel.Selection) bool {
	selection = uimodel.NormalizeSelection(selection)
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
	switch envConfig.Type {
	case config.EnvironmentTypeLocalAgent, config.EnvironmentTypeRemoteAgent:
		return true
	case config.EnvironmentTypeRuntime, config.EnvironmentTypeHost:
		fallthrough
	default:
		return false
	}
}

func (a *App) GetContributeMode(selection uimodel.Selection) bool {
	if a.contribute == nil {
		return false
	}
	return a.contribute.get(selection)
}

// SetContributeMode toggles contribute mode, cloning ERun first when enabling.
// The frontend manages the contribute tabs.
func (a *App) SetContributeMode(selection uimodel.Selection, on bool) (uiContributeState, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("set contribute mode", selection.Tenant, selection.Environment); err != nil {
		return uiContributeState{}, err
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

func (a *App) runEnsureErunClone(selection uimodel.Selection) error {
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return fmt.Errorf("resolve environment: %w", err)
	}
	ctx := a.context()
	mcpPort := config.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
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

func (a *App) resolveContributeAppPort(selection uimodel.Selection) (int, error) {
	if err := uimodel.ErrMissingTenantOrEnvironment("resolve contribute app port", selection.Tenant, selection.Environment); err != nil {
		return 0, err
	}
	if !a.GetContributeMode(selection) {
		return 0, fmt.Errorf("contribute mode is not enabled for %s/%s", selection.Tenant, selection.Environment)
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return 0, fmt.Errorf("resolve environment: %w", err)
	}
	port := config.ContributeAppPortForResult(result)
	if port <= 0 {
		return 0, fmt.Errorf("contribute-app port is not allocated for %s/%s", selection.Tenant, selection.Environment)
	}
	return port, nil
}

// StartContributeApp runs the headless app in the contribute tab and returns its URL.
func (a *App) StartContributeApp(selection uimodel.Selection) (uiContributeAppLaunch, error) {
	selection = uimodel.NormalizeSelection(selection)
	port, err := a.resolveContributeAppPort(selection)
	if err != nil {
		return uiContributeAppLaunch{}, err
	}

	// Run in the contribute tab so the user sees the build and can Ctrl-C it. Best-effort.
	a.sendCommandToContributeERunSession(selection, contributeAppLaunchCommand(port))

	if a.contributeApps == nil {
		a.contributeApps = newContributeAppForwards()
	}
	ctx := a.context()
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
	// window.open stays inside the WKWebView, so open through the Wails runtime.
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, url)
	}
	return uiContributeAppLaunch{
		URL:  url,
		Port: localPort,
	}, nil
}

func (a *App) stopContributeAppForward(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	if a.contributeApps != nil {
		if forward := a.contributeApps.take(selection); forward != nil {
			forward.stop()
		}
	}
	// Ctrl-C stops the headless process without killing the shell.
	a.sendCommandToContributeERunSession(selection, "\x03")
}

// The clone's development wrapper rebuilds the desktop before `erun app`.
func contributeAppLaunchCommand(port int) string {
	return fmt.Sprintf("erun app --headless --port %d\n", port)
}

func (a *App) sendCommandToContributeERunSession(selection uimodel.Selection, command string) {
	prefix := "contribute-erun\x00" + uimodel.SelectionKey(selection) + "\x00"
	var target *terminal.Session
	a.terminal.WithLock(func(l *terminal.Locked) {
		for key, managed := range l.Sessions() {
			if managed == nil || managed.Closed || managed.PTY == nil {
				continue
			}
			if strings.HasPrefix(key, prefix) {
				target = managed
				break
			}
		}
	})
	if target == nil {
		return
	}
	_, _ = io.WriteString(target.PTY, command)
}

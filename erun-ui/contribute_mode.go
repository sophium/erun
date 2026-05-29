package main

import (
	"context"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// uiContributeState is the JSON-facing read model the frontend uses to
// drive the per-env Contribute toggle.
type uiContributeState struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Enabled     bool   `json:"enabled"`
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

// uiContributeSnapshot returns the persisted contribute flags so the
// initial state payload can rehydrate per-env toggle state on app boot.
func (a *App) uiContributeSnapshot() map[string]bool {
	if a.contribute == nil {
		return map[string]bool{}
	}
	return a.contribute.snapshot()
}

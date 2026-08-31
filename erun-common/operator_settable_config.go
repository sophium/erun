package eruncommon

// OperatorSettableConfigField is one config field an operator is expected to
// set through a product surface -- a desktop dialog control, a CLI flag --
// rather than by hand-editing config.yaml. erun-integration's desktop-surface
// gate enumerates this registry alongside CLI commands, MCP tools, and API
// routes, and fails any non-exempt entry with no reference in the operator
// surface: the same dead end as a route nothing can call (root AGENTS.md §
// "Smooth, Seamless, No Dead Ends", failure mode 3). erun#1745 is the field
// that motivated this: OrchestratorEnvConfig.Role shipped with a reader
// (erun list) and no writer, and none of the gate's three existing
// enumerations (routes, MCP tools, CLI commands) could see the gap, because a
// bare config field is none of those three things.
//
// This registry is hand-maintained, the same discipline as
// erun-cli/cmd/command_tree.go's cliOnlyAgentFacingCommands: there is no
// language-level "this field is meant to be operator-set" marker to walk the
// way the Cobra command tree or the MCP tool table can be walked, so a config
// field an operator is meant to set is added here explicitly when it is
// introduced.
type OperatorSettableConfigField struct {
	// Name identifies the field for reporting, e.g. "OrchestratorEnvConfig.Role".
	Name string
	// Token is searched for, case-insensitively, as a plain substring of the
	// operator surface source (erun-ui/frontend/src, erun-console/src) --
	// same semantics as desktopsurface.Capability.Token.
	Token string
	// Internal, when true, is this field's explicit opt-out: it declares that
	// no operator surface is expected to set the field (for example, one only
	// a machine process writes), matching InternalAPIRoutes' discipline --
	// silence must never be how a field opts out of this registry.
	Internal bool
	// InternalReason documents why, required whenever Internal is true.
	InternalReason string
}

// OperatorSettableConfigFields is the registry TestDesktopSurfaceGate reads.
var OperatorSettableConfigFields = []OperatorSettableConfigField{
	{
		// Set by the desktop's Edit orchestrator dialog
		// (OrchestratorDialog.Environments.tsx) and by `erun orchestrator
		// set-role`. The frontend type mirroring OrchestratorEnvRole
		// (orchestratorsSlice.ts) is what this token matches.
		Name:  "OrchestratorEnvConfig.Role",
		Token: "orchestratorenvrole",
	},
}

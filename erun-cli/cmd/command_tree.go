package cmd

import "github.com/spf13/cobra"

// CommandTreeForAudit returns the same fully-wired command tree Execute()
// runs. It exists for tooling that needs to walk every registered command —
// the desktop-surface gate in erun-integration is the current caller —
// without reconstructing the tree from source text, which would drift from
// what newRootDependencies() actually wires (which commands exist, which are
// Hidden or Deprecated).
func CommandTreeForAudit() *cobra.Command {
	return newRootDependencies().rootCommand()
}

// cliOnlyAgentFacingCommands declares CLI commands that have no matching MCP
// tool yet are still not something a human reaches through the desktop app.
// This is the CLI-side twin of eruncommon.MCPToolDescriptor.AgentFacing: an
// explicit, reviewable exemption from the desktop-surface gate rather than a
// silent omission. Keyed by the command's path below "erun" (space-joined,
// e.g. "app restart").
//
// A command that is Hidden or Deprecated needs no entry here — the gate
// already treats those as internal, since both are themselves explicit,
// reviewable markers at the command's own definition site.
var cliOnlyAgentFacingCommands = map[string]bool{
	// Launches the desktop app itself; there is no desktop surface for
	// starting the desktop app before it exists.
	"app": true,
	// Restarts the already-running desktop process in place. Invoked by the
	// desktop's own Restart button or by an agent driving a rebuild, never a
	// separate user-facing action of its own.
	"app restart": true,
	// Runs erun as an MCP server / backend API process for an IDE or other
	// external tool to connect to. The desktop implements the equivalent
	// capability directly rather than shelling out to these launchers.
	"mcp": true,
	"api": true,
	// mcp's subcommands are a CLI-side debugging harness for the MCP wire
	// protocol itself (call a tool, list tools, mint a token, proxy stdio to
	// HTTP) -- built for a developer or an agent probing the protocol, not an
	// end-user action.
	"mcp call":  true,
	"mcp tools": true,
	"mcp token": true,
	"mcp proxy": true,
	// A legacy discovery path that re-registers the same build/push/deploy
	// commands under a "devops" namespace (erun-cli/cmd/root.go). Each leaf
	// here is the identical capability already declared at its top-level
	// path (build, push, deploy), so it needs no independent desktop surface.
	"devops container build": true,
	"devops container push":  true,
	"devops k8s deploy":      true,
}

// IsAgentFacingCLIOnlyCommand reports whether a CLI-only command path (no
// matching MCP tool) is declared exempt from needing a desktop entry point.
func IsAgentFacingCLIOnlyCommand(path string) bool {
	return cliOnlyAgentFacingCommands[path]
}

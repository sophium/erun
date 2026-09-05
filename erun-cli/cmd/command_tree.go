package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// CommandTreeForAudit returns the same fully-wired command tree Execute()
// runs. It exists for tooling that needs to walk every registered command —
// the desktop-surface gate in erun-integration is the current caller —
// without reconstructing the tree from source text, which would drift from
// what newRootDependencies() actually wires (which commands exist, which are
// Hidden or Deprecated).
func CommandTreeForAudit() *cobra.Command {
	return newRootDependencies().rootCommand()
}

// ContextSensitiveTopLevelCommands returns the top-level commands whose very
// presence in CommandTreeForAudit's tree is conditional on filesystem state at
// construction time: optionalBuildCommand/optionalPushCommand (root.go) omit
// "build"/"push" entirely — not just some of their flags — when no docker
// build context, build script, or linux package context resolves from the
// process's current working directory. A tree built from a cwd where neither
// resolves (an audit tool's own process cwd, a test binary's package
// directory) omits both nodes, which would make any other command's own
// Example/Long/Short text that cross-references `erun build ...`/
// `erun push ...` resolve against the root command instead and report every
// one of their flags as unregistered.
//
// Constructing them here with no real dependencies is safe because nothing
// calls RunE — only the same flag registration newBuildCmd/newRootPushCmd
// always run at construction time, unconditionally, regardless of the
// dependencies passed in. That registration is what a caller needs to
// recover each command's real, always-identical flag vocabulary even from a
// tree where the optional variant did not attach.
func ContextSensitiveTopLevelCommands() map[string]*cobra.Command {
	return map[string]*cobra.Command{
		"build": newBuildCmd(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, common.CloudDependencies{}),
		"push":  newRootPushCmd(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, common.CloudDependencies{}),
	}
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

// cliOnlyAgentFacingFlags is the flag-granularity twin of
// cliOnlyAgentFacingCommands: a flag that adds a dimension to an
// already-surfaced command yet is structurally about the CLI's own
// invocation rather than about the operation, so no desktop affordance could
// correspond to it. Keyed by "<command path> --<flag>", e.g.
// "open --no-shell".
//
// This list asserts a design decision that holds indefinitely. A flag that
// merely has no desktop surface *yet* belongs in knownUnsurfacedFlags below,
// which is a shrink-only record of gaps, not a second opt-out. Do not move a
// flag here to make a fresh gate failure go away.
var cliOnlyAgentFacingFlags = map[string]bool{
	// Prompt bypasses. Each exists only so a non-interactive CLI caller can
	// answer a prompt the CLI would otherwise draw; a GUI confirms in its own
	// dialog and never has a prompt to bypass.
	"init --confirm-environment":              true,
	"terraform apply --confirm-environment":   true,
	"terraform destroy --confirm-environment": true,
	// Local shell integration. Both shape what the CLI prints into the
	// operator's own terminal session (context/namespace switch commands, a
	// shell alias offer); neither names anything the app could do instead.
	"open --no-shell":        true,
	"open --no-alias-prompt": true,
	// Suppresses the CLI's remote version lookup so `erun version` still
	// answers offline. The desktop's version panel owns its own fetch policy.
	"version --no-registry": true,
	// Turns a report into a gate by changing the process exit code -- the one
	// thing a CLI has and a GUI does not (erun-cli/AGENTS.md § "Exit-Code
	// Contract"). The drift it reports is already surfaced; only the exit code
	// is CLI-shaped.
	"list --fail-on-drift": true,
	// Scripted-composition switches, in their own usage strings' words: they
	// let a caller chain expose/unexpose after another command without first
	// checking whether the project declares a platform block, and let one run
	// without a project checkout at all. Both describe how a script sequences
	// the command, not what the operator is exposing.
	"expose --skip-if-unconfigured":   true,
	"unexpose --skip-if-unconfigured": true,
	"expose --platform-namespace":     true,
	"unexpose --platform-namespace":   true,
	// In-pod repairs. These only take effect when doctor runs inside a runtime
	// pod, and the desktop app runs on the operator's own machine, never in
	// the pod -- so the surface that would host them does not exist. The two
	// value flags are inputs to --finish-remote-init and share its scope.
	"doctor --finish-remote-init":    true,
	"doctor --sync-config":           true,
	"doctor --remote-repository-url": true,
	"doctor --codecommit-ssh-key-id": true,
}

// knownUnsurfacedFlags baselines the flag-granularity gaps that existed the
// day the desktop-surface gate learned to reason about flags. It is a record
// of gaps that still exist, not a design decision that they should -- the
// same distinction erun-backend-api's KnownUnsurfacedRoutes draws against
// InternalAPIRoutes, and it is enforced the same way: the gate fails when an
// entry here gains a real operator surface without being deleted, so the list
// can only shrink. See erun-integration/AGENTS.md § "Desktop-surface gate".
//
// Adding a new flag here to clear a fresh gate failure is exactly what this
// list must not become. A new flag either gets an affordance or a
// cliOnlyAgentFacingFlags entry that says why it can never have one.
var knownUnsurfacedFlags = map[string]bool{
	// Build tuning: the desktop's build action always runs erun's default
	// fingerprint-based caching, with no "rebuild everything" escape hatch for
	// an operator who suspects a stale layer.
	"build --no-incremental": true,
	// Cloud-provider enrollment inputs. The desktop can sign in to an already
	// configured alias but does not run the guided init that collects these.
	"cloud init aws --role-name":         true,
	"cloud init cloudflare --api-token":  true,
	"cloud init cloudflare --token-name": true,
	// Optional EC2 placement inputs for a new remote context. The desktop's
	// context creation offers instance type and disk only.
	"context init --key-name":          true,
	"context init --security-group-id": true,
	"context init --subnet-id":         true,
	// MCP edge authentication and rollout patience. Both are real deploy-time
	// decisions an operator makes; the desktop's deploy affordance exposes
	// neither.
	"deploy --mcp-auth-public-key": true,
	"deploy --no-mcp-auth":         true,
	"deploy --rollout-timeout":     true,
	"init --mcp-auth-public-key":   true,
	// Remaining init inputs with no field in the desktop's setup flow.
	"init --codecommit-ssh-key-id": true,
	"init --project-root":          true,
	// Doctor's non-interactive repair and prune actions. The desktop has a
	// doctor panel, but these specific remedies are reachable only by running
	// the CLI with the flag.
	"doctor --prune-build-cache":              true,
	"doctor --prune-containers":               true,
	"doctor --prune-images":                   true,
	"doctor --repair-config":                  true,
	"doctor --repair-jetbrains-gateway":       true,
	"doctor --repair-workspace-sync":          true,
	"doctor --restore-config-from-backup":     true,
	"doctor --restore-env-config-from-backup": true,
	// Job execution limits. The desktop starts and watches jobs but never
	// offers the lease TTL or the captured-output cap the job will run under.
	"job start --lease-ttl":             true,
	"job start --max-output-bytes":      true,
	"job attach --lease-ttl":            true,
	"exec job start --lease-ttl":        true,
	"exec job start --max-output-bytes": true,
	// The per-environment certificate and ingress story. erun expose's TLS
	// provisioning needs the command redesigned around it before a desktop
	// affordance can be more than a bare form -- the same reasoning
	// KnownUnsurfacedRoutes records for the DNS-01 token mint.
	"expose --acme-email":               true,
	"expose --acme-server":              true,
	"expose --dns01-broker-url":         true,
	"expose --dns01-token-file":         true,
	"expose --dns01-webhook-group-name": true,
	"expose --ingress-class":            true,
	"expose --no-tls":                   true,
	"expose --tls-secret":               true,
	// Fleet/control-plane reporting dimensions on erun list, and the
	// gate-environment ordering upgrade honours. The desktop lists
	// environments but has no control-plane or gate-environment view.
	"list --control-planes":      true,
	"list --gate-environment":    true,
	"upgrade --gate-environment": true,
	// Root-disk shape for a platform-provisioned context, unlike the
	// instance type beside it.
	"platform provision --context-disk-size-gb": true,
	"platform provision --context-disk-type":    true,
}

// IsAgentFacingCLIOnlyFlag reports whether a flag on an operator-facing
// command is declared exempt from needing its own operator affordance.
func IsAgentFacingCLIOnlyFlag(commandPath, flag string) bool {
	return cliOnlyAgentFacingFlags[cliFlagKey(commandPath, flag)]
}

// IsKnownUnsurfacedFlag reports whether a flag is recorded in the shrink-only
// baseline of flag-granularity gaps that predate the gate seeing flags at all.
func IsKnownUnsurfacedFlag(commandPath, flag string) bool {
	return knownUnsurfacedFlags[cliFlagKey(commandPath, flag)]
}

// CLIFlagDeclarationKeys returns every key declared in either flag registry,
// so the gate can fail on an entry that no longer names a real flag rather
// than letting the declarations rot into fiction.
func CLIFlagDeclarationKeys() []string {
	keys := make([]string, 0, len(cliOnlyAgentFacingFlags)+len(knownUnsurfacedFlags))
	for key := range cliOnlyAgentFacingFlags {
		keys = append(keys, key)
	}
	for key := range knownUnsurfacedFlags {
		keys = append(keys, key)
	}
	return keys
}

// CLIFlagKey renders the registry key for a command path and flag name.
func CLIFlagKey(commandPath, flag string) string {
	return cliFlagKey(commandPath, flag)
}

func cliFlagKey(commandPath, flag string) string {
	return commandPath + " --" + flag
}

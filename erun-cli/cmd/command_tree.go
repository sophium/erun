package cmd

import (
	"github.com/sophium/erun/cmd/build"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/spf13/cobra"
)

// CommandTreeForAudit returns the fully-wired command tree Execute() runs.
func CommandTreeForAudit() *cobra.Command {
	return newRootDependencies().rootCommand()
}

// ContextSensitiveTopLevelCommands returns "build" and "push", which the audit tree
// omits when no build context resolves from the cwd. Nothing calls their RunE.
func ContextSensitiveTopLevelCommands() map[string]*cobra.Command {
	return map[string]*cobra.Command{
		"build": build.NewBuildCmd(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, cloud.CloudDependencies{}),
		"push":  build.NewRootPushCmd(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, cloud.CloudDependencies{}),
	}
}

// cliOnlyAgentFacingCommands exempts CLI commands with no MCP tool from the
// desktop-surface gate. Hidden or Deprecated commands need no entry.
var cliOnlyAgentFacingCommands = map[string]bool{
	// Tears down an environment's infrastructure; deliberately kept off the desktop.
	"terraform destroy": true,
	// No desktop surface can start the desktop app.
	"app": true,
	// Invoked by the desktop's Restart button or an agent, not a user action.
	"app restart": true,
	// Server launchers for external tools; the desktop implements these directly.
	"mcp": true,
	"api": true,
	// MCP wire-protocol debugging harness.
	"mcp call":  true,
	"mcp tools": true,
	"mcp token": true,
	"mcp proxy": true,
	// Legacy aliases of the top-level build/push/deploy.
	"devops container build": true,
	"devops container push":  true,
	"devops k8s deploy":      true,
}

// IsAgentFacingCLIOnlyCommand reports whether a CLI-only command is exempt from the desktop-surface gate.
func IsAgentFacingCLIOnlyCommand(path string) bool {
	return cliOnlyAgentFacingCommands[path]
}

// outputJSONAliasCommands declares every command whose own --json flag is an
// alias of the global --output json, which must also reach its result.
var outputJSONAliasCommands = map[string]bool{
	"activity ai-session status": true,
	"activity lease list":        true,
	"activity lease take":        true,
	"activity sample":            true,
	"activity status":            true,
	"activity stop-ready":        true,
	"exec diff":                  true,
	"idle":                       true,
	"whip":                       true,
}

// IsOutputJSONAliasCommand reports whether a command's --json is a declared alias of --output json.
func IsOutputJSONAliasCommand(path string) bool {
	return outputJSONAliasCommands[path]
}

// cliOnlyAgentFacingFlags exempts flags about the CLI's own invocation, which no
// desktop affordance could correspond to. Gaps belong in knownUnsurfacedFlags.
var cliOnlyAgentFacingFlags = map[string]bool{
	// Prompt bypasses; a GUI confirms in its own dialog.
	"init --confirm-environment":            true,
	"terraform apply --confirm-environment": true,
	// Shape output in the operator's own terminal.
	"open --no-shell":        true,
	"open --no-alias-prompt": true,
	// Offline `erun version`; the desktop owns its own fetch policy.
	"version --no-registry": true,
	// Changes only the exit code.
	"list --fail-on-drift": true,
	// Script sequencing switches.
	"expose --skip-if-unconfigured":   true,
	"unexpose --skip-if-unconfigured": true,
	"expose --platform-namespace":     true,
	"unexpose --platform-namespace":   true,
	// In-pod repairs; the desktop never runs in the pod.
	"doctor --finish-remote-init":    true,
	"doctor --sync-config":           true,
	"doctor --remote-repository-url": true,
	"doctor --codecommit-ssh-key-id": true,
	// Chooses this machine's port allocation for a new environment; the desktop never pulls.
	"platform env pull --port-range-start": true,
	// Agent job coordination: the finish check and the gate's environment claim.
	"job start --handoff":        true,
	"job start --exclusive":      true,
	"exec job start --handoff":   true,
	"exec job start --exclusive": true,
	// Points init at a directory from the shell; the desktop picks its own.
	"init --project-root": true,
}

// knownUnsurfacedFlags is a shrink-only baseline of flag gaps; the gate fails
// when an entry gains a surface without being deleted. Never add to it.
var knownUnsurfacedFlags = map[string]bool{
	"build --no-incremental":                    true,
	"cloud init aws --role-name":                true,
	"cloud init cloudflare --api-token":         true,
	"cloud init cloudflare --token-name":        true,
	"context init --key-name":                   true,
	"context init --security-group-id":          true,
	"context init --subnet-id":                  true,
	"deploy --mcp-auth-public-key":              true,
	"deploy --no-mcp-auth":                      true,
	"deploy --rollout-timeout":                  true,
	"init --mcp-auth-public-key":                true,
	"init --codecommit-ssh-key-id":              true,
	"doctor --prune-build-cache":                true,
	"doctor --prune-containers":                 true,
	"doctor --prune-images":                     true,
	"doctor --repair-config":                    true,
	"doctor --repair-jetbrains-gateway":         true,
	"doctor --repair-workspace-sync":            true,
	"doctor --restore-config-from-backup":       true,
	"doctor --restore-env-config-from-backup":   true,
	"job start --lease-ttl":                     true,
	"job start --max-output-bytes":              true,
	"job attach --lease-ttl":                    true,
	"exec job start --lease-ttl":                true,
	"exec job start --max-output-bytes":         true,
	"expose --acme-email":                       true,
	"expose --acme-server":                      true,
	"expose --dns01-broker-url":                 true,
	"expose --dns01-token-file":                 true,
	"expose --dns01-webhook-group-name":         true,
	"expose --ingress-class":                    true,
	"expose --no-tls":                           true,
	"expose --tls-secret":                       true,
	"list --control-planes":                     true,
	"list --gate-environment":                   true,
	"upgrade --gate-environment":                true,
	"platform provision --context-disk-size-gb": true,
	"platform provision --context-disk-type":    true,
	"expose --services-zone":                    true,
	"unexpose --services-zone":                  true,
	"review list --author-user-id":              true,
}

// IsAgentFacingCLIOnlyFlag reports whether a flag is exempt from needing a desktop affordance.
func IsAgentFacingCLIOnlyFlag(commandPath, flag string) bool {
	// Every command previews with --dry-run; the desktop confirms in its own dialogs.
	if flag == "dry-run" {
		return true
	}
	return cliOnlyAgentFacingFlags[cliFlagKey(commandPath, flag)]
}

// IsKnownUnsurfacedFlag reports whether a flag is in the shrink-only gap baseline.
func IsKnownUnsurfacedFlag(commandPath, flag string) bool {
	return knownUnsurfacedFlags[cliFlagKey(commandPath, flag)]
}

// CLIFlagDeclarationKeys returns every declared flag key, so stale entries fail the gate.
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

// CLIFlagKey renders the registry key for a command path and flag.
func CLIFlagKey(commandPath, flag string) string {
	return cliFlagKey(commandPath, flag)
}

func cliFlagKey(commandPath, flag string) string {
	return commandPath + " --" + flag
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorMCPServer is one Claude Code MCP server entry: a linked env's erun
// MCP edge (emcp in the pod, exposing raw/diff/build/push/deploy/outputs_*),
// reached by launching `erun mcp proxy` over stdio. The proxy mints a bearer per
// request, so no credential is written here and a session cannot lose its envs
// partway through to an aged-out token.
type orchestratorMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type orchestratorMCPConfig struct {
	MCPServers map[string]orchestratorMCPServer `json:"mcpServers"`
}

// mcpPortResolver is a seam so the config assembly is unit testable without a
// live config store.
type mcpPortResolver func(tenant, environment string) int

// orchestratorMCPSkip is one linked environment that produced no MCP server
// entry, and why. Carried out of the builder rather than dropped: an
// orchestrator is told by its own operating contract to know which environments
// are its own, so an environment silently missing from its toolset is a
// falsehood it cannot detect from the inside (#1185).
type orchestratorMCPSkip struct {
	Label  string
	Reason string
}

// buildOrchestratorMCPConfig assembles the per-env MCP server map, keyed
// "<tenant>-<environment>". An env is skipped (not an error) when its MCP port
// does not resolve — that env is not wired for MCP at all — so an orchestrator
// still gets the envs it can reach. Every skip is returned alongside, so a
// PARTIAL skip is reportable: previously only the all-envs-failed case produced
// any signal, and a session missing one of two environments looked identical to
// a healthy one until its first call into the missing env.
func buildOrchestratorMCPConfig(envs []eruncommon.OrchestratorEnvConfig, executable string, mcpPort mcpPortResolver) (orchestratorMCPConfig, []orchestratorMCPSkip) {
	servers := map[string]orchestratorMCPServer{}
	var skipped []orchestratorMCPSkip
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return orchestratorMCPConfig{MCPServers: servers}, nil
	}
	for _, env := range envs {
		tenant := strings.TrimSpace(env.Tenant)
		environment := strings.TrimSpace(env.Environment)
		if tenant == "" || environment == "" {
			skipped = append(skipped, orchestratorMCPSkip{
				Label:  orchestratorEnvLabel(tenant, environment),
				Reason: "the linked entry names no tenant or environment",
			})
			continue
		}
		if mcpPort(tenant, environment) <= 0 {
			skipped = append(skipped, orchestratorMCPSkip{
				Label:  orchestratorEnvLabel(tenant, environment),
				Reason: "it resolved no MCP port",
			})
			continue
		}
		servers[tenant+"-"+environment] = orchestratorMCPServer{
			Type:    "stdio",
			Command: executable,
			Args:    []string{"mcp", "proxy", "--tenant", tenant, "--environment", environment},
		}
	}
	return orchestratorMCPConfig{MCPServers: servers}, skipped
}

// orchestratorEnvLabel names an environment for an operator-facing line, staying
// readable when the config entry itself is the thing that is malformed.
func orchestratorEnvLabel(tenant, environment string) string {
	switch {
	case tenant == "" && environment == "":
		return "an unnamed linked entry"
	case tenant == "":
		return "?/" + environment
	case environment == "":
		return tenant + "/?"
	default:
		return tenant + "/" + environment
	}
}

// The two ways an orchestrator ends up with linked environments but none of
// their tools. They stay distinct because the operator's fix differs: one is a
// missing erun install, the other a linked environment that no longer resolves.
var (
	errOrchestratorMCPExecutable = errors.New("the erun executable could not be resolved")
	errOrchestratorMCPNoPort     = errors.New("no linked environment resolved an MCP port")
)

// orchestratorMCPUnwiredNotice is the operator-facing line for an orchestrator
// that launched without the tools for the environments it is linked to. It names
// the cause and the matching recovery, since the session otherwise looks healthy
// right up to the first environment call.
func orchestratorMCPUnwiredNotice(name string, err error) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	cause, recovery := err.Error(), "Restart the orchestrator once that is resolved."
	switch {
	case errors.Is(err, errOrchestratorMCPExecutable):
		cause = errOrchestratorMCPExecutable.Error()
		recovery = "Install the erun command line tool, then restart the orchestrator."
	case errors.Is(err, errOrchestratorMCPNoPort):
		cause = errOrchestratorMCPNoPort.Error()
		recovery = "Check its linked environments still exist, then restart the orchestrator."
	}
	return fmt.Sprintf("%s started without its environment tools: %s. %s", label, cause, recovery)
}

// orchestratorMCPPartialNotice is the operator-facing line for an orchestrator
// that got SOME of its environments' tools. Distinct from the unwired notice
// because the session is usable and will look entirely healthy: the missing
// environment surfaces only as a tool that is not there, which an agent reads as
// "not linked" rather than "failed to wire" (#1185).
func orchestratorMCPPartialNotice(name string, wired int, skipped []orchestratorMCPSkip) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "The orchestrator"
	}
	missing := make([]string, 0, len(skipped))
	for _, skip := range skipped {
		missing = append(missing, fmt.Sprintf("%s (%s)", skip.Label, skip.Reason))
	}
	return fmt.Sprintf("%s started with tools for %d of %d linked environments. Missing: %s. Check those environments still exist, then restart the orchestrator.",
		label, wired, wired+len(skipped), strings.Join(missing, "; "))
}

// writeOrchestratorMCPConfig writes a per-orchestrator Claude Code --mcp-config
// file wiring each linked env's erun MCP into the orchestrator session, so it
// drives its envs through the MCP rather than raw kubectl. Returns "" with an
// error naming why when nothing could be wired, so the caller skips
// --mcp-config and can tell the operator which fix applies.
func (a *App) writeOrchestratorMCPConfig(id string, envs []eruncommon.OrchestratorEnvConfig) (string, []orchestratorMCPSkip, error) {
	// An orchestrator with no linked envs has nothing to wire, and that is normal.
	if len(envs) == 0 {
		return "", nil, nil
	}
	// No erun binary means no proxy to launch, so the session launches without
	// its envs rather than with entries that would fail on first use.
	executable, err := eruncommon.ResolveErunExecutable()
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w", errOrchestratorMCPExecutable, err)
	}
	config, skipped := buildOrchestratorMCPConfig(envs, executable,
		func(tenant, environment string) int {
			ports, portErr := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, tenant, environment)
			if portErr != nil {
				return 0
			}
			return ports.MCP
		},
	)
	if len(config.MCPServers) == 0 {
		return "", skipped, errOrchestratorMCPNoPort
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", skipped, err
	}
	path := orchestratorMCPConfigPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", skipped, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", skipped, err
	}
	return path, skipped, nil
}

// orchestratorMCPConfigPath is a per-orchestrator sibling of
// orchestrator-restore.json under UserConfigDir()/ERun, so each orchestrator's
// env-MCP wiring is isolated (the shared orchestrators workspace can't hold a
// per-orchestrator .mcp.json).
func orchestratorMCPConfigPath(id string) string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-mcp-"+sanitizeOrchestratorFileID(id)+".json")
}

func sanitizeOrchestratorFileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, id)
}

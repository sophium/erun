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

// buildOrchestratorMCPConfig assembles the per-env MCP server map, keyed
// "<tenant>-<environment>". An env is skipped (not an error) when its MCP port
// does not resolve — that env is not wired for MCP at all — so an orchestrator
// still gets the envs it can reach.
func buildOrchestratorMCPConfig(envs []eruncommon.OrchestratorEnvConfig, executable string, mcpPort mcpPortResolver) orchestratorMCPConfig {
	servers := map[string]orchestratorMCPServer{}
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return orchestratorMCPConfig{MCPServers: servers}
	}
	for _, env := range envs {
		tenant := strings.TrimSpace(env.Tenant)
		environment := strings.TrimSpace(env.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		if mcpPort(tenant, environment) <= 0 {
			continue
		}
		servers[tenant+"-"+environment] = orchestratorMCPServer{
			Type:    "stdio",
			Command: executable,
			Args:    []string{"mcp", "proxy", "--tenant", tenant, "--environment", environment},
		}
	}
	return orchestratorMCPConfig{MCPServers: servers}
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

// writeOrchestratorMCPConfig writes a per-orchestrator Claude Code --mcp-config
// file wiring each linked env's erun MCP into the orchestrator session, so it
// drives its envs through the MCP rather than raw kubectl. Returns "" with an
// error naming why when nothing could be wired, so the caller skips
// --mcp-config and can tell the operator which fix applies.
func (a *App) writeOrchestratorMCPConfig(id string, envs []eruncommon.OrchestratorEnvConfig) (string, error) {
	// An orchestrator with no linked envs has nothing to wire, and that is normal.
	if len(envs) == 0 {
		return "", nil
	}
	// No erun binary means no proxy to launch, so the session launches without
	// its envs rather than with entries that would fail on first use.
	executable, err := eruncommon.ResolveErunExecutable()
	if err != nil {
		return "", fmt.Errorf("%w: %w", errOrchestratorMCPExecutable, err)
	}
	config := buildOrchestratorMCPConfig(envs, executable,
		func(tenant, environment string) int {
			ports, portErr := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, tenant, environment)
			if portErr != nil {
				return 0
			}
			return ports.MCP
		},
	)
	if len(config.MCPServers) == 0 {
		return "", errOrchestratorMCPNoPort
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	path := orchestratorMCPConfigPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
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

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorMCPServer is one Claude Code HTTP MCP server entry: a linked env's
// erun MCP edge (emcp in the pod, exposing raw/diff/build/push/deploy/outputs_*),
// reached over the desktop's local port-forward and authenticated with a
// desktop-signed bearer.
type orchestratorMCPServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type orchestratorMCPConfig struct {
	MCPServers map[string]orchestratorMCPServer `json:"mcpServers"`
}

// mcpPortResolver and bearerSigner are seams so the config assembly is unit
// testable without a live config store or signing identity.
type mcpPortResolver func(tenant, environment string) int
type bearerSigner func(tenant, environment string) string

// buildOrchestratorMCPConfig assembles the per-env MCP server map, keyed
// "<tenant>-<environment>". An env is skipped (not an error) when its MCP port
// or bearer cannot be resolved, so a partially-signable orchestrator still wires
// the envs it can.
func buildOrchestratorMCPConfig(envs []eruncommon.OrchestratorEnvConfig, mcpPort mcpPortResolver, bearer bearerSigner) orchestratorMCPConfig {
	servers := map[string]orchestratorMCPServer{}
	for _, env := range envs {
		tenant := strings.TrimSpace(env.Tenant)
		environment := strings.TrimSpace(env.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		port := mcpPort(tenant, environment)
		if port <= 0 {
			continue
		}
		token := strings.TrimSpace(bearer(tenant, environment))
		if token == "" {
			continue
		}
		servers[tenant+"-"+environment] = orchestratorMCPServer{
			Type:    "http",
			URL:     fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
			Headers: map[string]string{"Authorization": "Bearer " + token},
		}
	}
	return orchestratorMCPConfig{MCPServers: servers}
}

// writeOrchestratorMCPConfig writes a per-orchestrator Claude Code --mcp-config
// file wiring each linked env's erun MCP into the orchestrator session, so it
// drives its envs through the MCP rather than raw kubectl. Returns "" (no file)
// when no env resolves a port + bearer, so the caller skips --mcp-config.
// Bearers are minted fresh at each launch/resume (they expire; a longer-lived
// refresh is a follow-up).
func (a *App) writeOrchestratorMCPConfig(id string, envs []eruncommon.OrchestratorEnvConfig) (string, error) {
	config := buildOrchestratorMCPConfig(envs,
		func(tenant, environment string) int {
			ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, tenant, environment)
			if err != nil {
				return 0
			}
			return ports.MCP
		},
		a.mcpBearer,
	)
	if len(config.MCPServers) == 0 {
		return "", nil
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

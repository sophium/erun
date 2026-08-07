package main

import (
	"encoding/json"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// orchestratorTestPort is the port stub the config builder is driven with;
// extracted from the test body so its own branching stays under the cyclop
// threshold.
func orchestratorTestPort(tenant, _ string) int {
	switch tenant {
	case "petios":
		return 17400
	case "erun":
		return 17300
	default:
		return 0
	}
}

func orchestratorTestEnvs() []eruncommon.OrchestratorEnvConfig {
	return []eruncommon.OrchestratorEnvConfig{
		{Tenant: "petios", Environment: "rihards-win-develop"},
		{Tenant: "erun", Environment: "main"},
		{Tenant: "noport", Environment: "x"}, // skipped: port 0
		{Tenant: "", Environment: "z"},       // skipped: blank tenant
	}
}

func TestBuildOrchestratorMCPConfig(t *testing.T) {
	config := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort)

	if len(config.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d: %v", len(config.MCPServers), config.MCPServers)
	}
	petios, ok := config.MCPServers["petios-rihards-win-develop"]
	if !ok {
		t.Fatalf("missing petios server: %v", config.MCPServers)
	}
	if petios.Type != "stdio" || petios.Command != "/opt/erun/bin/erun" {
		t.Fatalf("unexpected petios server: %+v", petios)
	}
	wantArgs := "mcp proxy --tenant petios --environment rihards-win-develop"
	if got := strings.Join(petios.Args, " "); got != wantArgs {
		t.Fatalf("petios args = %q, want %q", got, wantArgs)
	}
	if _, ok := config.MCPServers["erun-main"]; !ok {
		t.Fatalf("missing erun server")
	}
	for _, skipped := range []string{"noport-x", "-z"} {
		if _, ok := config.MCPServers[skipped]; ok {
			t.Fatalf("expected %s to be skipped", skipped)
		}
	}
}

// The written file is what a launched orchestrator reads, and it must never be a
// place a bearer can leak from: an MCP client cannot refresh a header it was
// configured with, so the fix for the expiry was to stop writing one at all.
func TestBuildOrchestratorMCPConfigCarriesNoCredential(t *testing.T) {
	config := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	for _, forbidden := range []string{"Bearer", "Authorization", "authorization", "headers", "token"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("config carries %q:\n%s", forbidden, data)
		}
	}
}

// Without an erun binary there is no proxy to launch, so the whole map is empty
// and the caller skips --mcp-config rather than writing entries that fail on
// first use.
func TestBuildOrchestratorMCPConfigSkipsEveryEnvWithoutAnExecutable(t *testing.T) {
	config := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "  ", orchestratorTestPort)
	if len(config.MCPServers) != 0 {
		t.Fatalf("expected no servers without an executable, got %v", config.MCPServers)
	}
}

func TestBuildOrchestratorLaunchInjectsMCPConfig(t *testing.T) {
	_, withMCP := buildOrchestratorLaunch("linux", "", false, "", "", "/cfg/orchestrator-mcp-petios3.json")
	if joined := strings.Join(withMCP, " "); !strings.Contains(joined, `--mcp-config "/cfg/orchestrator-mcp-petios3.json"`) {
		t.Fatalf("expected --mcp-config in launch, got: %s", joined)
	}

	_, withoutMCP := buildOrchestratorLaunch("linux", "", false, "", "", "")
	if joined := strings.Join(withoutMCP, " "); strings.Contains(joined, "--mcp-config") {
		t.Fatalf("expected no --mcp-config when path empty, got: %s", joined)
	}
}

func TestSanitizeOrchestratorFileID(t *testing.T) {
	for in, want := range map[string]string{
		"petios3":     "petios3",
		"va1":         "va1",
		"a/b c":       "a-b-c",
		"":            "default",
		"weird..name": "weird--name",
	} {
		if got := sanitizeOrchestratorFileID(in); got != want {
			t.Fatalf("sanitizeOrchestratorFileID(%q) = %q, want %q", in, got, want)
		}
	}
}

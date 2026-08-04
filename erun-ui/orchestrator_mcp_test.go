package main

import (
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func TestBuildOrchestratorMCPConfig(t *testing.T) {
	envs := []eruncommon.OrchestratorEnvConfig{
		{Tenant: "petios", Environment: "rihards-win-develop"},
		{Tenant: "erun", Environment: "main"},
		{Tenant: "noport", Environment: "x"},   // skipped: port 0
		{Tenant: "nobearer", Environment: "y"}, // skipped: empty bearer
		{Tenant: "", Environment: "z"},         // skipped: blank tenant
	}
	port := func(tenant, _ string) int {
		switch tenant {
		case "petios":
			return 17400
		case "erun":
			return 17300
		case "nobearer":
			return 18000
		default:
			return 0
		}
	}
	bearer := func(tenant, _ string) string {
		if tenant == "nobearer" {
			return ""
		}
		return "tok-" + tenant
	}

	config := buildOrchestratorMCPConfig(envs, port, bearer)

	if len(config.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d: %v", len(config.MCPServers), config.MCPServers)
	}
	petios, ok := config.MCPServers["petios-rihards-win-develop"]
	if !ok {
		t.Fatalf("missing petios server: %v", config.MCPServers)
	}
	if petios.Type != "http" || petios.URL != "http://127.0.0.1:17400/mcp" {
		t.Fatalf("unexpected petios server: %+v", petios)
	}
	if got := petios.Headers["Authorization"]; got != "Bearer tok-petios" {
		t.Fatalf("unexpected petios auth header: %q", got)
	}
	if _, ok := config.MCPServers["erun-main"]; !ok {
		t.Fatalf("missing erun server")
	}
	for _, skipped := range []string{"noport-x", "nobearer-y", "-z"} {
		if _, ok := config.MCPServers[skipped]; ok {
			t.Fatalf("expected %s to be skipped", skipped)
		}
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

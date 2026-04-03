package cmd

import (
	"testing"

	erunmcp "github.com/sophium/erun/erun-mcp"
)

func TestNewMCPCmdDefaultsToLocalHTTP(t *testing.T) {
	cmd := NewMCPCmd(Dependencies{})

	host, err := cmd.Flags().GetString("host")
	if err != nil {
		t.Fatalf("GetString(host) failed: %v", err)
	}
	port, err := cmd.Flags().GetInt("port")
	if err != nil {
		t.Fatalf("GetInt(port) failed: %v", err)
	}
	path, err := cmd.Flags().GetString("path")
	if err != nil {
		t.Fatalf("GetString(path) failed: %v", err)
	}

	if host != erunmcp.DefaultHost || port != erunmcp.DefaultPort || path != erunmcp.DefaultPath {
		t.Fatalf("unexpected defaults: host=%q port=%d path=%q", host, port, path)
	}
}

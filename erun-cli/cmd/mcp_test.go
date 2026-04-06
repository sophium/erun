package cmd

import (
	"bytes"
	"testing"

	erunmcp "github.com/sophium/erun/erun-mcp"
)

func TestNewMCPCmdDefaultsToLocalHTTP(t *testing.T) {
	cmd := NewMCPCmd(Dependencies{}, nil)

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

func TestMCPCmdDryRunPrintsTraceWithoutStartingServer(t *testing.T) {
	cmd := NewMCPCmd(Dependencies{}, nil)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--dry-run", "--host", "0.0.0.0", "--port", "17000", "--path", "/mcp"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("expected no server output during dry-run, got %q", got)
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("[dry-run] erun mcp --host 0.0.0.0 --port 17000 --path /mcp")) {
		t.Fatalf("expected dry-run trace, got %q", got)
	}
}

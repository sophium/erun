package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
	erunmcp "github.com/sophium/erun/erun-mcp"
)

func TestRunUsesDefaultMCPConfig(t *testing.T) {
	var gotInfo eruncommon.BuildInfo
	var gotCfg erunmcp.HTTPConfig
	var gotRuntime erunmcp.RuntimeConfig

	exitCode := run(nil, new(bytes.Buffer), eruncommon.BuildInfo{Version: "1.2.3"}, func(ctx context.Context, info eruncommon.BuildInfo, cfg erunmcp.HTTPConfig, runtime erunmcp.RuntimeConfig) error {
		if ctx == nil {
			t.Fatal("expected non-nil context")
		}
		gotInfo = info
		gotCfg = cfg
		gotRuntime = runtime
		return nil
	})
	if exitCode != 0 {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if gotInfo.Version != "1.2.3" {
		t.Fatalf("unexpected build info: %+v", gotInfo)
	}
	if gotCfg.Host != erunmcp.DefaultHost || gotCfg.Port != erunmcp.DefaultPort || gotCfg.Path != erunmcp.DefaultPath {
		t.Fatalf("unexpected MCP config: %+v", gotCfg)
	}
	if gotRuntime.Context != (erunmcp.RuntimeContext{}) {
		t.Fatalf("unexpected runtime config: %+v", gotRuntime)
	}
}

func TestRunPassesResolvedFlags(t *testing.T) {
	var gotCfg erunmcp.HTTPConfig
	var gotRuntime erunmcp.RuntimeConfig

	exitCode := run([]string{"--host", "0.0.0.0", "--port", "17001", "--path", "custom", "--tenant", "tenant-a", "--environment", "dev", "--repo-path", "/tmp/project", "--kubernetes-context", "cluster-dev", "--namespace", "tenant-a-dev"}, new(bytes.Buffer), eruncommon.BuildInfo{}, func(_ context.Context, _ eruncommon.BuildInfo, cfg erunmcp.HTTPConfig, runtime erunmcp.RuntimeConfig) error {
		gotCfg = cfg
		gotRuntime = runtime
		return nil
	})
	if exitCode != 0 {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if gotCfg.Host != "0.0.0.0" || gotCfg.Port != 17001 || gotCfg.Path != "custom" {
		t.Fatalf("unexpected MCP config: %+v", gotCfg)
	}
	if gotRuntime.Context.Tenant != "tenant-a" || gotRuntime.Context.Environment != "dev" || gotRuntime.Context.RepoPath != "/tmp/project" || gotRuntime.Context.KubernetesContext != "cluster-dev" || gotRuntime.Context.Namespace != "tenant-a-dev" {
		t.Fatalf("unexpected runtime config: %+v", gotRuntime)
	}
}

func TestRunReturnsFailureWhenServerFails(t *testing.T) {
	stderr := new(bytes.Buffer)

	exitCode := run(nil, stderr, eruncommon.BuildInfo{}, func(context.Context, eruncommon.BuildInfo, erunmcp.HTTPConfig, erunmcp.RuntimeConfig) error {
		return errors.New("boom")
	})
	if exitCode != 1 {
		t.Fatalf("expected failure exit code, got %d", exitCode)
	}
	if got := stderr.String(); !strings.Contains(got, "erun mcp listening on 127.0.0.1:17000/mcp") || !strings.HasSuffix(got, "boom\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

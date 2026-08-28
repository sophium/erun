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
	var gotMetricsCfg erunmcp.MetricsConfig
	var gotRuntime erunmcp.RuntimeConfig

	exitCode := run(nil, new(bytes.Buffer), eruncommon.BuildInfo{Version: "1.2.3"}, func(ctx context.Context, info eruncommon.BuildInfo, cfg erunmcp.HTTPConfig, metricsCfg erunmcp.MetricsConfig, runtime erunmcp.RuntimeConfig) error {
		if ctx == nil {
			t.Fatal("expected non-nil context")
		}
		gotInfo = info
		gotCfg = cfg
		gotMetricsCfg = metricsCfg
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
	assertMetricsConfig(t, gotMetricsCfg, erunmcp.DefaultMetricsHost, erunmcp.DefaultMetricsPort, true)
	if gotRuntime.Context != (erunmcp.RuntimeContext{}) {
		t.Fatalf("unexpected runtime config: %+v", gotRuntime)
	}
}

func assertMetricsConfig(t *testing.T, got erunmcp.MetricsConfig, wantHost string, wantPort int, wantEnabled bool) {
	t.Helper()
	if got.Host != wantHost || got.Port != wantPort || got.Enabled != wantEnabled {
		t.Fatalf("unexpected metrics config: %+v", got)
	}
}

func TestRunPassesResolvedFlags(t *testing.T) {
	var gotCfg erunmcp.HTTPConfig
	var gotMetricsCfg erunmcp.MetricsConfig
	var gotRuntime erunmcp.RuntimeConfig

	exitCode := run([]string{
		"--host", "0.0.0.0", "--port", "17001", "--path", "custom",
		"--metrics-host", "0.0.0.0", "--metrics-port", "9101", "--metrics-enabled=false",
		"--tenant", "tenant-a", "--environment", "dev", "--repo-path", "/tmp/project", "--kubernetes-context", "cluster-dev", "--namespace", "tenant-a-dev",
	}, new(bytes.Buffer), eruncommon.BuildInfo{}, func(_ context.Context, _ eruncommon.BuildInfo, cfg erunmcp.HTTPConfig, metricsCfg erunmcp.MetricsConfig, runtime erunmcp.RuntimeConfig) error {
		gotCfg = cfg
		gotMetricsCfg = metricsCfg
		gotRuntime = runtime
		return nil
	})
	if exitCode != 0 {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}
	if gotCfg.Host != "0.0.0.0" || gotCfg.Port != 17001 || gotCfg.Path != "custom" {
		t.Fatalf("unexpected MCP config: %+v", gotCfg)
	}
	assertMetricsConfig(t, gotMetricsCfg, "0.0.0.0", 9101, false)
	assertRuntimeContext(t, gotRuntime.Context)
}

func assertRuntimeContext(t *testing.T, got erunmcp.RuntimeContext) {
	t.Helper()
	if got.Tenant != "tenant-a" || got.Environment != "dev" || got.RepoPath != "/tmp/project" || got.KubernetesContext != "cluster-dev" || got.Namespace != "tenant-a-dev" {
		t.Fatalf("unexpected runtime context: %+v", got)
	}
}

func TestRunRefusesSpaceSeparatedBoolFlagValue(t *testing.T) {
	stderr := new(bytes.Buffer)
	called := false

	exitCode := run([]string{
		"--metrics-enabled", "true",
		"--tenant", "tenant-a", "--environment", "dev",
	}, stderr, eruncommon.BuildInfo{}, func(context.Context, eruncommon.BuildInfo, erunmcp.HTTPConfig, erunmcp.MetricsConfig, erunmcp.RuntimeConfig) error {
		called = true
		return nil
	})
	if exitCode != 2 {
		t.Fatalf("expected refusal exit code 2, got %d", exitCode)
	}
	if called {
		t.Fatal("expected server not to start when leftover positional arguments remain")
	}
	if got := stderr.String(); !strings.Contains(got, `"true"`) || !strings.Contains(got, "--flag=value") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestRunReturnsFailureWhenServerFails(t *testing.T) {
	stderr := new(bytes.Buffer)

	exitCode := run(nil, stderr, eruncommon.BuildInfo{}, func(context.Context, eruncommon.BuildInfo, erunmcp.HTTPConfig, erunmcp.MetricsConfig, erunmcp.RuntimeConfig) error {
		return errors.New("boom")
	})
	if exitCode != 1 {
		t.Fatalf("expected failure exit code, got %d", exitCode)
	}
	if got := stderr.String(); !strings.Contains(got, "erun mcp listening on 127.0.0.1:17000/mcp") || !strings.HasSuffix(got, "boom\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

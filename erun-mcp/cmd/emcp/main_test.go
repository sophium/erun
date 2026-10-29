package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-mcp"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

func TestRunUsesDefaultMCPConfig(t *testing.T) {
	var gotInfo model.BuildInfo
	var gotCfg erunmcp.HTTPConfig
	var gotMetricsCfg erunmcp.MetricsConfig
	var gotRuntime runtimecfg.RuntimeConfig

	exitCode := run(nil, new(bytes.Buffer), model.BuildInfo{Version: "1.2.3"}, func(ctx context.Context, info model.BuildInfo, cfg erunmcp.HTTPConfig, metricsCfg erunmcp.MetricsConfig, runtime runtimecfg.RuntimeConfig) error {
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
	if gotRuntime.Context != (toolio.RuntimeContext{}) {
		t.Fatalf("unexpected runtime config: %+v", gotRuntime)
	}
}

func assertMetricsConfig(t *testing.T, got erunmcp.MetricsConfig, wantHost string, wantPort int, wantEnabled bool) {
	t.Helper()
	if got.Host != wantHost || got.Port != wantPort || got.Enabled != wantEnabled {
		t.Fatalf("unexpected metrics config: %+v", got)
	}
}

func TestRunRefusesSpaceSeparatedBoolFlagValue(t *testing.T) {
	stderr := new(bytes.Buffer)
	called := false

	exitCode := run([]string{
		"--metrics-enabled", "true",
		"--tenant", "tenant-a", "--environment", "dev",
	}, stderr, model.BuildInfo{}, func(context.Context, model.BuildInfo, erunmcp.HTTPConfig, erunmcp.MetricsConfig, runtimecfg.RuntimeConfig) error {
		called = true
		return nil
	})
	if exitCode != 2 {
		t.Fatalf("expected refusal exit code 2, got %d", exitCode)
	}
	if called {
		t.Fatal("expected server not to start when leftover positional arguments remain")
	}
	got := stderr.String()
	if !strings.Contains(got, `"true"`) || !strings.Contains(got, "--flag=value") {
		t.Fatalf("unexpected stderr: %q", got)
	}
	if !strings.Contains(got, "flags after it were never applied: --tenant tenant-a --environment dev") {
		t.Fatalf("expected stderr to name the dropped flags, got: %q", got)
	}
}

func TestRunRefusesUnrecognizedPositionalArgument(t *testing.T) {
	stderr := new(bytes.Buffer)
	called := false

	exitCode := run([]string{
		"bogus", "--tenant", "tenant-a",
	}, stderr, model.BuildInfo{}, func(context.Context, model.BuildInfo, erunmcp.HTTPConfig, erunmcp.MetricsConfig, runtimecfg.RuntimeConfig) error {
		called = true
		return nil
	})
	if exitCode != 2 {
		t.Fatalf("expected refusal exit code 2, got %d", exitCode)
	}
	if called {
		t.Fatal("expected server not to start when leftover positional arguments remain")
	}
	got := stderr.String()
	if !strings.Contains(got, `"bogus"`) {
		t.Fatalf("expected stderr to name the unrecognized argument, got: %q", got)
	}
	if !strings.Contains(got, "flags after it were never applied: --tenant tenant-a") {
		t.Fatalf("expected stderr to name the dropped flags, got: %q", got)
	}
	if !strings.Contains(got, "likely cause") {
		t.Fatalf("expected the bool-flag explanation to be phrased as a guess, got: %q", got)
	}
}

func TestRunReturnsFailureWhenServerFails(t *testing.T) {
	stderr := new(bytes.Buffer)

	exitCode := run(nil, stderr, model.BuildInfo{}, func(context.Context, model.BuildInfo, erunmcp.HTTPConfig, erunmcp.MetricsConfig, runtimecfg.RuntimeConfig) error {
		return errors.New("boom")
	})
	if exitCode != 1 {
		t.Fatalf("expected failure exit code, got %d", exitCode)
	}
	if got := stderr.String(); !strings.Contains(got, "erun mcp listening on 127.0.0.1:17000/mcp") || !strings.HasSuffix(got, "boom\n") {
		t.Fatalf("unexpected stderr: %q", got)
	}
}

func TestWithRuntimeDefaultsWiresEverySeamAndEnsuresTheNamespaceTwicePerDeploy(t *testing.T) {
	ensured := 0
	deployed := 0
	var got runtimecfg.RuntimeConfig
	runner := withRuntimeDefaults(func(_ context.Context, _ model.BuildInfo, _ erunmcp.HTTPConfig, _ erunmcp.MetricsConfig, runtime runtimecfg.RuntimeConfig) error {
		got = runtime
		return nil
	})
	if err := runner(context.Background(), model.BuildInfo{}, erunmcp.HTTPConfig{}, erunmcp.MetricsConfig{}, runtimecfg.RuntimeConfig{
		EnsureKubernetesNamespace: func(string, string) error { ensured++; return nil },
		DeployHelmChart:           func(helmdeploy.HelmDeployParams) error { deployed++; return nil },
	}); err != nil {
		t.Fatalf("runner failed: %v", err)
	}

	unwired := []bool{
		got.Store == nil, got.BuildScriptRunner == nil, got.BuildDockerImage == nil, got.PushDockerImage == nil,
		got.DeployHelmChart == nil, got.DeleteKubernetesNamespace == nil, got.WaitForRemoteRuntime == nil,
		got.RunRemoteCommand == nil, got.ResolveRuntimeRegistryVersions == nil,
	}
	if slices.Contains(unwired, true) {
		t.Fatalf("expected every runtime seam to be wired, got %+v", got)
	}
	if err := got.DeployHelmChart(helmdeploy.HelmDeployParams{}); err != nil {
		t.Fatalf("DeployHelmChart failed: %v", err)
	}
	if ensured != 2 || deployed != 1 {
		t.Fatalf("ensured=%d deployed=%d, want the namespace ensured twice around one deploy", ensured, deployed)
	}
}

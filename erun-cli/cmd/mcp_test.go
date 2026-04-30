package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestNewMCPCmdDefaultsToLocalHTTP(t *testing.T) {
	root := newTestRootCmd(testRootDeps{})
	cmd, _, err := root.Find([]string{"mcp"})
	if err != nil {
		t.Fatalf("Find(mcp) failed: %v", err)
	}

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

	if host != defaultMCPHost || port != defaultMCPPort || path != defaultMCPPath {
		t.Fatalf("unexpected defaults: host=%q port=%d path=%q", host, port, path)
	}
}

func TestMCPCmdDryRunPrintsTraceWithoutStartingServer(t *testing.T) {
	repoPath := t.TempDir()
	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath: repoPath,
			toolConfig: common.ERunConfig{
				DefaultTenant: "tenant-a",
			},
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "mcp", "--dry-run", "--host", "0.0.0.0", "--port", "17000", "--path", "/mcp"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := stdout.String(); got != "" {
		t.Fatalf("expected no server output during dry-run, got %q", got)
	}
	wantTrace := "emcp --host 0.0.0.0 --port 17000 --path /mcp --tenant tenant-a --environment local --repo-path " + repoPath + " --kubernetes-context cluster-dev --namespace tenant-a-local"
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte(wantTrace)) {
		t.Fatalf("expected dry-run trace, got %q", got)
	}
}

func TestMCPCmdStartsEMCP(t *testing.T) {
	started := false
	var gotArgs []string
	repoPath := t.TempDir()

	cmd := newTestRootCmd(testRootDeps{
		Store: openCommandStore{
			repoPath: repoPath,
		},
		LaunchMCP: func(stdin io.Reader, stdout, stderr io.Writer, args []string) error {
			started = true
			gotArgs = append([]string(nil), args...)
			return nil
		},
	})
	cmd.SetArgs([]string{"mcp", "tenant-a", "dev", "--host", "0.0.0.0", "--port", "17001", "--path", "custom"})

	requireNoError(t, cmd.Execute(), "Execute failed")
	if !started {
		t.Fatal("expected emcp to be launched")
	}
	wantArgs := []string{"--host", "0.0.0.0", "--port", "17001", "--path", "custom", "--tenant", "tenant-a", "--environment", "dev", "--repo-path", repoPath, "--kubernetes-context", "cluster-dev", "--namespace", "tenant-a-dev"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("unexpected emcp args: got=%v want=%v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("unexpected emcp args: got=%v want=%v", gotArgs, wantArgs)
		}
	}
}

func TestMCPCmdUsesEnvironmentLocalPortByDefault(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	tenantAPath := filepath.Join(t.TempDir(), "tenant-a")
	tenantBPath := filepath.Join(t.TempDir(), "tenant-b")
	for _, dir := range []string{tenantAPath, tenantBPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir repo: %v", err)
		}
	}

	requireNoError(t, common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}), "save erun config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-a", ProjectRoot: tenantAPath, DefaultEnvironment: "local"}), "save tenant-a config")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "tenant-b", ProjectRoot: tenantBPath, DefaultEnvironment: "dev"}), "save tenant-b config")
	requireNoError(t, common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "local", RepoPath: tenantAPath, KubernetesContext: "cluster-a"}), "save tenant-a env")
	requireNoError(t, common.SaveEnvConfig("tenant-b", common.EnvConfig{Name: "dev", RepoPath: tenantBPath, KubernetesContext: "cluster-b"}), "save tenant-b env")

	cmd := newTestRootCmd(testRootDeps{})
	stderr := new(bytes.Buffer)
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "mcp", "tenant-b", "dev", "--dry-run"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("--port 17100")) {
		t.Fatalf("expected environment-scoped MCP port in trace, got %q", got)
	}
}

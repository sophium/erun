package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// bundledDesktopOutputEnvVar marks the re-exec'd child of
// TestWriteOrchestratorMCPConfigFromBundledDesktop and tells it where to leave
// the config it wrote, so the parent can assert on real written bytes.
const bundledDesktopOutputEnvVar = "ERUN_TEST_BUNDLED_DESKTOP_OUTPUT"

// The shipped desktop runs from <root>/bin/ERun.app/Contents/MacOS/erun-app and
// launches its proxies with the erun beside the bundle at <root>/bin/erun. Only
// a process actually running from that path exercises the resolution, so this
// re-execs itself from a copy there — with an empty PATH and no ERUN_ERUN_BIN,
// so the sibling binary is the only thing that can resolve.
func TestWriteOrchestratorMCPConfigFromBundledDesktop(t *testing.T) {
	if output := strings.TrimSpace(os.Getenv(bundledDesktopOutputEnvVar)); output != "" {
		writeBundledDesktopMCPConfig(t, output)
		return
	}
	root := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	program := copyTestFile(t, self, filepath.Join(root, "bin", "ERun.app", "Contents", "MacOS", "erun-app"))
	sibling := copyTestFile(t, self, filepath.Join(root, "bin", "erun"))
	output := filepath.Join(root, "written.json")

	home := t.TempDir()
	child := exec.Command(program, "-test.run", "^"+t.Name()+"$")
	child.Env = []string{
		"PATH=",
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		bundledDesktopOutputEnvVar + "=" + output,
	}
	combined, runErr := child.CombinedOutput()
	if runErr != nil {
		t.Fatalf("bundled desktop child failed: %v\n%s", runErr, combined)
	}
	data, readErr := os.ReadFile(output)
	if readErr != nil {
		t.Fatalf("the bundled desktop wrote no MCP config: %v\n%s", readErr, combined)
	}
	assertBundledDesktopMCPConfig(t, data, sibling)
}

// writeBundledDesktopMCPConfig is the child half: it runs the real wiring from
// inside the bundle and hands the bytes back through output.
func writeBundledDesktopMCPConfig(t *testing.T, output string) {
	t.Helper()
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	path, err := app.writeOrchestratorMCPConfig("petios", []eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "dev"},
	})
	if err != nil {
		t.Fatalf("writeOrchestratorMCPConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config %s: %v", path, err)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		t.Fatalf("hand config back: %v", err)
	}
}

func assertBundledDesktopMCPConfig(t *testing.T, data []byte, wantCommand string) {
	t.Helper()
	var config orchestratorMCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse written config: %v\n%s", err, data)
	}
	server, ok := config.MCPServers["frs-dev"]
	if !ok {
		t.Fatalf("written config has no server for the linked env:\n%s", data)
	}
	if server.Type != "stdio" || server.Command != wantCommand {
		t.Fatalf("server = %+v, want a stdio entry commanding %q", server, wantCommand)
	}
	if got, want := strings.Join(server.Args, " "), "mcp proxy --tenant frs --environment dev"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"Bearer", "Authorization", "authorization", "headers", "token"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("written config carries %q:\n%s", forbidden, data)
		}
	}
}

func copyTestFile(t *testing.T, source, destination string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(destination), err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatalf("open %s: %v", source, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create %s: %v", destination, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatalf("copy %s: %v", destination, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close %s: %v", destination, err)
	}
	return destination
}

// An orchestrator whose linked environments produced no MCP server has to say
// so: the session launches and looks healthy, and the operator would otherwise
// only find out one missing tool call at a time. No linked environments is the
// ordinary case and stays quiet.
func TestSpawnOrchestratorSignalsUnwiredEnvironments(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		envs     []eruncommon.OrchestratorEnvConfig
		wantNote string
	}{
		{
			name: "linked env resolves an MCP port",
			envs: []eruncommon.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}},
		},
		{
			name:     "linked env resolves no MCP port",
			envs:     []eruncommon.OrchestratorEnvConfig{{Tenant: "ghost", Environment: "missing"}},
			wantNote: errOrchestratorMCPNoPort.Error(),
		},
		{
			name: "no linked envs",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// The override keeps the erun binary resolvable regardless of what sits
			// beside the test binary, so the port is the only variable under test.
			t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
			app := orchestratorTestApp(t)
			defer app.shutdown(context.Background())
			emits := newCapturedEmits()
			app.emitFn = emits.fn()

			if _, err := app.spawnOrchestratorSession("petios", "Petios", testCase.envs, "", "", false, 80, 24); err != nil {
				t.Fatalf("spawnOrchestratorSession: %v", err)
			}
			assertUnwiredNotice(t, emits.events(appNotificationEvent), testCase.wantNote)
		})
	}
}

func assertUnwiredNotice(t *testing.T, events []any, wantNote string) {
	t.Helper()
	if wantNote == "" {
		if len(events) != 0 {
			t.Fatalf("expected no notification, got %+v", events)
		}
		return
	}
	if len(events) != 1 {
		t.Fatalf("expected one notification, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning so the banner persists", payload.Kind)
	}
	if !strings.Contains(payload.Message, wantNote) || !strings.Contains(payload.Message, "Petios") {
		t.Fatalf("message %q does not name the orchestrator and the cause %q", payload.Message, wantNote)
	}
}

// The two causes need different fixes, so the notice must not collapse them into
// one "could not be wired".
func TestOrchestratorMCPUnwiredNoticeNamesTheCause(t *testing.T) {
	executable := orchestratorMCPUnwiredNotice("Petios", errors.Join(errOrchestratorMCPExecutable, errors.New("not on PATH")))
	if !strings.Contains(executable, errOrchestratorMCPExecutable.Error()) || !strings.Contains(executable, "Install the erun command line tool") {
		t.Fatalf("executable notice does not name its recovery: %q", executable)
	}
	port := orchestratorMCPUnwiredNotice("", errOrchestratorMCPNoPort)
	if !strings.Contains(port, errOrchestratorMCPNoPort.Error()) || !strings.Contains(port, "linked environments still exist") {
		t.Fatalf("port notice does not name its recovery: %q", port)
	}
	if strings.Contains(port, errOrchestratorMCPExecutable.Error()) {
		t.Fatalf("port notice blames the executable: %q", port)
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

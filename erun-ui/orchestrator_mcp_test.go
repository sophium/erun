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

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

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

func orchestratorTestEnvs() []configpkg.OrchestratorEnvConfig {
	return []configpkg.OrchestratorEnvConfig{
		{Tenant: "petios", Environment: "rihards-win-develop"},
		{Tenant: "erun", Environment: "main"},
		{Tenant: "noport", Environment: "x"}, // skipped: port 0
		{Tenant: "", Environment: "z"},       // skipped: blank tenant
	}
}

// orchestratorTestAlwaysReachable makes every wired env probe as reachable.
func orchestratorTestAlwaysReachable(int) bool { return true }

// orchestratorTestNoHostEnvType reports every env as local-agent, so none takes the host skip path.
func orchestratorTestNoHostEnvType(string, string) configpkg.EnvironmentType {
	return configpkg.EnvironmentTypeLocalAgent
}

// orchestratorTestEnvTypeHosts marks hostTenant's environments as host and the rest as local-agent.
func orchestratorTestEnvTypeHosts(hostTenant string) mcpEnvTypeResolver {
	return func(tenant, _ string) configpkg.EnvironmentType {
		if tenant == hostTenant {
			return configpkg.EnvironmentTypeHost
		}
		return configpkg.EnvironmentTypeLocalAgent
	}
}

func TestBuildOrchestratorMCPConfig(t *testing.T) {
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)

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

// An MCP client cannot refresh a configured header, so none is written.
func TestBuildOrchestratorMCPConfigCarriesNoCredential(t *testing.T) {
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)
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

func TestBuildOrchestratorMCPConfigSkipsEveryEnvWithoutAnExecutable(t *testing.T) {
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "  ", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)
	if len(config.MCPServers) != 0 {
		t.Fatalf("expected no servers without an executable, got %v", config.MCPServers)
	}
}

func TestBuildOrchestratorLaunchInjectsMCPConfig(t *testing.T) {
	_, withMCP := buildOrchestratorLaunch("linux", defaultOrchestratorLaunch(), "", "/cfg/orchestrator-mcp-petios3.json")
	if joined := strings.Join(withMCP, " "); !strings.Contains(joined, `--mcp-config '/cfg/orchestrator-mcp-petios3.json'`) {
		t.Fatalf("expected --mcp-config in launch, got: %s", joined)
	}

	_, withoutMCP := buildOrchestratorLaunch("linux", defaultOrchestratorLaunch(), "", "")
	if joined := strings.Join(withoutMCP, " "); strings.Contains(joined, "--mcp-config") {
		t.Fatalf("expected no --mcp-config when path empty, got: %s", joined)
	}
}

// bundledDesktopOutputEnvVar tells the re-exec'd child where to write its config.
const bundledDesktopOutputEnvVar = "ERUN_TEST_BUNDLED_DESKTOP_OUTPUT"

// Re-execs from <root>/bin/ERun.app/Contents/MacOS with an empty PATH and no
// ERUN_ERUN_BIN, so only the sibling <root>/bin/erun can resolve.
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

func writeBundledDesktopMCPConfig(t *testing.T, output string) {
	t.Helper()
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	path, _, _, err := app.writeOrchestratorMCPConfig("petios", []configpkg.OrchestratorEnvConfig{
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

// No linked environments is the ordinary case and stays quiet.
func TestSpawnOrchestratorSignalsUnwiredEnvironments(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		envs     []configpkg.OrchestratorEnvConfig
		wantNote string
	}{
		{
			name: "linked env resolves an MCP port",
			envs: []configpkg.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}},
		},
		{
			name:     "linked env resolves no MCP port",
			envs:     []configpkg.OrchestratorEnvConfig{{Tenant: "ghost", Environment: "missing"}},
			wantNote: errOrchestratorMCPNoPort.Error(),
		},
		{
			name: "no linked envs",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
			app := orchestratorTestApp(t)
			defer app.shutdown(context.Background())
			emits := newCapturedEmits()
			app.emitFn = emits.fn()

			spawn := orchestratorSpawn{id: "petios", name: "Petios", envs: testCase.envs, cols: 80, rows: 24}
			if _, err := app.spawnOrchestratorSession(spawn); err != nil {
				t.Fatalf("spawnOrchestratorSession: %v", err)
			}
			assertUnwiredNotice(t, emits.events(uimodel.AppNotificationEvent), testCase.wantNote)
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
	payload, ok := events[0].(uimodel.AppNotificationPayload)
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

// The two causes need different fixes, so the notice must not collapse them.
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

func TestOrchestratorMCPUnwiredActionNamesTheControl(t *testing.T) {
	if got := orchestratorMCPUnwiredAction(errors.Join(errOrchestratorMCPExecutable, errors.New("not on PATH"))); got != notificationActionInstallAndRestartOrchestrator {
		t.Fatalf("executable-missing action = %q, want %q", got, notificationActionInstallAndRestartOrchestrator)
	}
	if got := orchestratorMCPUnwiredAction(errOrchestratorMCPNoPort); got != notificationActionRestartOrchestrator {
		t.Fatalf("no-port action = %q, want %q", got, notificationActionRestartOrchestrator)
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

// A partial skip must be reported: an orchestrator reads an absent tool as "not linked".
func TestBuildOrchestratorMCPConfigReportsEverySkip(t *testing.T) {
	config, skipped, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)

	if len(config.MCPServers) != 2 {
		t.Fatalf("wired %d servers, want 2", len(config.MCPServers))
	}
	if len(skipped) != 2 {
		t.Fatalf("reported %d skips, want 2 (the unresolved port and the blank entry): %+v", len(skipped), skipped)
	}

	byLabel := map[string]string{}
	for _, skip := range skipped {
		byLabel[skip.Label] = skip.Reason
	}
	reason, ok := byLabel["noport/x"]
	if !ok {
		t.Fatalf("no skip reported for the environment that resolved no port: %+v", skipped)
	}
	if !strings.Contains(reason, "MCP port") {
		t.Errorf("skip reason %q does not name the cause", reason)
	}
	// A missing tenant labels as "?/z".
	if reason, ok := byLabel["?/z"]; !ok {
		t.Errorf("a malformed linked entry must still be reported, not silently dropped: %+v", skipped)
	} else if !strings.Contains(reason, "no tenant or environment") {
		t.Errorf("skip reason %q does not name the cause", reason)
	}
}

// The proxy recovers transient edge outages per call, so an unreachable edge is
// wired anyway and reported as unreachable, not skipped.
func TestBuildOrchestratorMCPConfigStillWiresAnUnreachableEdge(t *testing.T) {
	unreachableAlways := func(int) bool { return false }
	config, skipped, unreachable := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestNoHostEnvType, unreachableAlways)

	if len(config.MCPServers) != 2 {
		t.Fatalf("expected both resolvable envs still wired despite an unreachable edge, got %d: %v", len(config.MCPServers), config.MCPServers)
	}
	if _, ok := config.MCPServers["erun-main"]; !ok {
		t.Fatalf("expected erun-main still wired even though its edge is unreachable: %v", config.MCPServers)
	}
	if _, ok := config.MCPServers["petios-rihards-win-develop"]; !ok {
		t.Fatalf("expected petios still wired even though its edge is unreachable: %v", config.MCPServers)
	}
	if len(skipped) != 2 {
		t.Fatalf("expected the pre-existing skip count unaffected by reachability, got %d: %+v", len(skipped), skipped)
	}
	if len(unreachable) != 2 {
		t.Fatalf("expected both wired envs reported unreachable, got %d: %+v", len(unreachable), unreachable)
	}
}

func TestBuildOrchestratorMCPConfigReportsNoUnreachableEdgeWhenAllAnswer(t *testing.T) {
	_, _, unreachable := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)
	if len(unreachable) != 0 {
		t.Fatalf("expected no unreachable envs when every edge answers, got %+v", unreachable)
	}
}

func TestOrchestratorMCPPartialNoticeNamesWhatIsMissing(t *testing.T) {
	notice := orchestratorMCPPartialNotice("erun-issues", 1, 2, nil, []orchestratorMCPSkip{
		{Label: "petios/rihards-review", Reason: "it resolved no MCP port"},
	})

	for _, want := range []string{"erun-issues", "1 of 2", "petios/rihards-review", "resolved no MCP port", "restart"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "no MCP edge is expected") {
		t.Errorf("notice mentions a host env when none is linked:\n%s", notice)
	}
}

// A host env is not a problem but still counts in the denominator.
func TestOrchestratorMCPPartialNoticeCountsHostEnvsInItsDenominator(t *testing.T) {
	notice := orchestratorMCPPartialNotice("erun-issues", 1, 3,
		[]orchestratorMCPSkip{{Label: "frs/host", Reason: "it is a host environment, which has no pod and so no MCP edge to reach", HostEnv: true}},
		[]orchestratorMCPSkip{{Label: "petios/rihards-review", Reason: "it resolved no MCP port"}},
	)

	for _, want := range []string{"1 of 3", "frs/host", "no MCP edge is expected"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}
	// The host env is the reason for the shortfall, not a problem needing a restart.
	if strings.Contains(notice, "frs/host (") {
		t.Errorf("notice lists the host env among the missing problems:\n%s", notice)
	}
}

// A test app's store resolves no ports, so every environment is skipped deterministically.
func TestWriteOrchestratorMCPConfigCarriesSkipsEvenWhenNothingWired(t *testing.T) {
	// The executable is resolved before the no-port path, so pin it.
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	path, skipped, _, err := app.writeOrchestratorMCPConfig("nothing-wirable", []configpkg.OrchestratorEnvConfig{
		{Tenant: "ghost", Environment: "one"},
		{Tenant: "ghost", Environment: "two"},
	})
	if !errors.Is(err, errOrchestratorMCPNoPort) {
		t.Fatalf("err = %v, want errOrchestratorMCPNoPort", err)
	}
	if strings.TrimSpace(path) != "" {
		t.Errorf("path = %q, want empty when nothing wired", path)
	}
	if len(skipped) != 2 {
		t.Fatalf("reported %d skips, want 2 so the notice can name them: %+v", len(skipped), skipped)
	}
	for _, skip := range skipped {
		if !strings.HasPrefix(skip.Label, "ghost/") {
			t.Errorf("skip label %q does not name the environment", skip.Label)
		}
		if strings.TrimSpace(skip.Reason) == "" {
			t.Errorf("skip for %s carries no reason", skip.Label)
		}
	}
}

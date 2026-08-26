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

// orchestratorTestAlwaysReachable stands in for a.deps.canReachMCPEndpoint in
// tests that are not themselves about reachability, so every wired env probes
// as reachable rather than depending on a real port-forward.
func orchestratorTestAlwaysReachable(int) bool { return true }

func TestBuildOrchestratorMCPConfig(t *testing.T) {
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestAlwaysReachable)

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
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestAlwaysReachable)
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
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "  ", orchestratorTestPort, orchestratorTestAlwaysReachable)
	if len(config.MCPServers) != 0 {
		t.Fatalf("expected no servers without an executable, got %v", config.MCPServers)
	}
}

func TestBuildOrchestratorLaunchInjectsMCPConfig(t *testing.T) {
	_, withMCP := buildOrchestratorLaunch("linux", "", false, "", "", "/cfg/orchestrator-mcp-petios3.json")
	if joined := strings.Join(withMCP, " "); !strings.Contains(joined, `--mcp-config '/cfg/orchestrator-mcp-petios3.json'`) {
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

	path, _, _, err := app.writeOrchestratorMCPConfig("petios", []eruncommon.OrchestratorEnvConfig{
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

			spawn := orchestratorSpawn{id: "petios", name: "Petios", envs: testCase.envs, cols: 80, rows: 24}
			if _, err := app.spawnOrchestratorSession(spawn); err != nil {
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

// TestBuildOrchestratorMCPConfigReportsEverySkip is the regression test for
// #1185. The builder skipped an environment whose MCP port did not resolve and
// dropped the fact on the floor, so a PARTIAL skip was silent on every channel:
// no notification, no log line, and nothing the session itself could see. An
// orchestrator is told by its own contract to know which environments are its
// own, and an absent tool reads as "not linked" rather than "failed to wire" --
// so it cannot detect this from the inside.
//
// The pre-existing test above asserted the skip happened and said nothing about
// it being reported, which is exactly why nothing caught this.
func TestBuildOrchestratorMCPConfigReportsEverySkip(t *testing.T) {
	config, skipped, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestAlwaysReachable)

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
	// The fixture's malformed entry names an environment but no tenant, so it
	// labels as "?/z" -- the placeholder marks which half is missing rather than
	// hiding the entry entirely.
	if reason, ok := byLabel["?/z"]; !ok {
		t.Errorf("a malformed linked entry must still be reported, not silently dropped: %+v", skipped)
	} else if !strings.Contains(reason, "no tenant or environment") {
		t.Errorf("skip reason %q does not name the cause", reason)
	}
}

// TestBuildOrchestratorMCPConfigStillWiresAnUnreachableEdge is the regression
// test for the corrected scope of a retracted design. The originally filed issue would have
// counted a dead port-forward as unwired and dropped the environment for the
// whole session -- retracted after reading erun-common/mcp_proxy.go, which
// already recovers a transient edge outage per call. The corrected behaviour:
// an env whose edge does not answer a probe at launch is wired anyway, and
// reported as unreachable, never as skipped.
func TestBuildOrchestratorMCPConfigStillWiresAnUnreachableEdge(t *testing.T) {
	unreachableAlways := func(int) bool { return false }
	config, skipped, unreachable := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, unreachableAlways)

	if len(config.MCPServers) != 2 {
		t.Fatalf("expected both resolvable envs still wired despite an unreachable edge, got %d: %v", len(config.MCPServers), config.MCPServers)
	}
	if _, ok := config.MCPServers["erun-main"]; !ok {
		t.Fatalf("expected erun-main still wired even though its edge is unreachable: %v", config.MCPServers)
	}
	if _, ok := config.MCPServers["petios-rihards-win-develop"]; !ok {
		t.Fatalf("expected petios still wired even though its edge is unreachable: %v", config.MCPServers)
	}
	// The pre-existing skip count (unresolved port, blank tenant) must be
	// unaffected by reachability -- those two never had a port to probe.
	if len(skipped) != 2 {
		t.Fatalf("expected the pre-existing skip count unaffected by reachability, got %d: %+v", len(skipped), skipped)
	}
	if len(unreachable) != 2 {
		t.Fatalf("expected both wired envs reported unreachable, got %d: %+v", len(unreachable), unreachable)
	}
}

// A reachable edge must not be reported as unreachable -- otherwise every
// orchestrator launch would carry a spurious warning.
func TestBuildOrchestratorMCPConfigReportsNoUnreachableEdgeWhenAllAnswer(t *testing.T) {
	_, _, unreachable := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestAlwaysReachable)
	if len(unreachable) != 0 {
		t.Fatalf("expected no unreachable envs when every edge answers, got %+v", unreachable)
	}
}

func TestSingleOrchestratorMCPUnreachableEnv(t *testing.T) {
	if _, _, ok := singleOrchestratorMCPUnreachableEnv(nil); ok {
		t.Fatal("expected no match for zero unreachable envs")
	}
	if _, _, ok := singleOrchestratorMCPUnreachableEnv([]orchestratorMCPUnreachable{
		{Label: "frs/dev"}, {Label: "frs/staging"},
	}); ok {
		t.Fatal("expected no match for more than one unreachable env")
	}
	tenant, environment, ok := singleOrchestratorMCPUnreachableEnv([]orchestratorMCPUnreachable{{Label: "frs/dev"}})
	if !ok || tenant != "frs" || environment != "dev" {
		t.Fatalf("got tenant=%q environment=%q ok=%v, want frs/dev/true", tenant, environment, ok)
	}
}

func TestOrchestratorMCPUnreachableNoticeNamesTheEnvironments(t *testing.T) {
	if got := orchestratorMCPUnreachableNotice("Petios", nil); got != "" {
		t.Fatalf("expected no notice when nothing is unreachable, got %q", got)
	}
	notice := orchestratorMCPUnreachableNotice("Petios", []orchestratorMCPUnreachable{{Label: "frs/dev"}})
	for _, want := range []string{"Petios", "frs/dev", "not answering"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice does not mention %q: %q", want, notice)
		}
	}
}

// TestWireOrchestratorMCPWiresAnUnreachableEnvAndSaysSo exercises the full
// wiring path: the written config still carries the unreachable env, and the
// operator gets a notice distinct from the partial-skip one.
func TestWireOrchestratorMCPWiresAnUnreachableEnvAndSaysSo(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app, _ := orchestratorTestAppWithReachability(t, func(int) bool { return false })
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	path := app.wireOrchestratorMCP("petios", "Petios", []eruncommon.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}})
	if strings.TrimSpace(path) == "" {
		t.Fatal("expected an MCP config path even though the edge is unreachable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "frs-dev") {
		t.Fatalf("expected the unreachable env still wired into the config:\n%s", data)
	}

	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected exactly one notice about the unreachable edge, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if !strings.Contains(payload.Message, "frs/dev") || !strings.Contains(payload.Message, "not answering") {
		t.Fatalf("notice does not name the environment and the reason: %q", payload.Message)
	}
	// Exactly one unreachable env is the unambiguous case: the notice can only
	// mean this env, so it carries the deploy action and is tagged with it
	// (#1390) rather than leaving the "deploy or reopen" remedy unreachable.
	wantTag := [4]string{"frs", "dev", notificationSourceOrchestratorEdgeUnreachable, notificationActionDeploy}
	gotTag := [4]string{payload.Tenant, payload.Environment, payload.Source, payload.Action}
	if gotTag != wantTag {
		t.Fatalf("notice tenant/environment/source/action = %+v, want %+v", gotTag, wantTag)
	}
}

// TestWireOrchestratorMCPMultipleUnreachableEnvsCarryNoAction locks the
// ambiguous case: when more than one linked env's edge is unreachable, no
// single env can own the notice's action, so it falls back to the plain
// app-level notice with no action rather than guessing which env to deploy.
func TestWireOrchestratorMCPMultipleUnreachableEnvsCarryNoAction(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app, _ := orchestratorTestAppWithReachability(t, func(int) bool { return false })
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	app.wireOrchestratorMCP("petios", "Petios", []eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "dev"},
		{Tenant: "frs", Environment: "laptop"},
	})

	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected exactly one notice about the unreachable edges, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Tenant != "" || payload.Environment != "" || payload.Action != "" {
		t.Fatalf("notice = %+v, want no tenant/environment/action tag when several envs are unreachable", payload)
	}
}

// TestOrchestratorMCPPartialNoticeNamesWhatIsMissing: the notice is the only
// thing that tells an operator a usable-looking session is missing an
// environment, so it has to name which one and why -- "some tools are missing"
// is not actionable.
func TestOrchestratorMCPPartialNoticeNamesWhatIsMissing(t *testing.T) {
	notice := orchestratorMCPPartialNotice("erun-issues", 1, []orchestratorMCPSkip{
		{Label: "petios/rihards-review", Reason: "it resolved no MCP port"},
	})

	for _, want := range []string{"erun-issues", "1 of 2", "petios/rihards-review", "resolved no MCP port", "restart"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}
}

// TestWriteOrchestratorMCPConfigCarriesSkipsEvenWhenNothingWired: the total
// failure already had a signal (errOrchestratorMCPNoPort), but it could not say
// WHICH environments failed or why. Returning the skips alongside the error
// means the unwired notice can name them too, not just the partial one.
//
// Deterministic on purpose: a test app's store resolves no ports, so every
// environment is skipped. Asserting the partial case at this level would depend
// on ambient store state and be flaky, which is worse than not testing it here
// -- the builder tests above cover the partial split with an injected resolver.
func TestWriteOrchestratorMCPConfigCarriesSkipsEvenWhenNothingWired(t *testing.T) {
	// Pin the executable seam. Without it this test only passes where an erun
	// binary happens to sit on PATH: writeOrchestratorMCPConfig resolves the
	// executable BEFORE it reaches the no-port path, so on a host without one it
	// returns errOrchestratorMCPExecutable and the assertion below never sees the
	// skips it exists to check. The build's own test stage has no erun on PATH,
	// which is where that surfaced.
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))

	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	path, skipped, _, err := app.writeOrchestratorMCPConfig("nothing-wirable", []eruncommon.OrchestratorEnvConfig{
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

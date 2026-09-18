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

// orchestratorTestNoHostEnvType is the env-type stub for the tests that are
// not themselves about a host environment. It answers "local-agent" for every
// linked env, which is both realistic for orchestratorTestEnvs' fixture and
// the important half of the assertion: no env takes the host skip path, so
// those tests' skip counts stay exactly what they were before a host env
// could be linked at all.
func orchestratorTestNoHostEnvType(string, string) eruncommon.EnvironmentType {
	return eruncommon.EnvironmentTypeLocalAgent
}

// orchestratorTestEnvTypeHosts marks hostTenant's environments as host and
// every other tenant's as local-agent, so one fixture exercises the host skip
// and the ordinary wiring path side by side -- the case that matters, since
// the defect was a host env being wired as an edge that can never answer.
func orchestratorTestEnvTypeHosts(hostTenant string) mcpEnvTypeResolver {
	return func(tenant, _ string) eruncommon.EnvironmentType {
		if tenant == hostTenant {
			return eruncommon.EnvironmentTypeHost
		}
		return eruncommon.EnvironmentTypeLocalAgent
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

// The written file is what a launched orchestrator reads, and it must never be a
// place a bearer can leak from: an MCP client cannot refresh a header it was
// configured with, so the fix for the expiry was to stop writing one at all.
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

// Without an erun binary there is no proxy to launch, so the whole map is empty
// and the caller skips --mcp-config rather than writing entries that fail on
// first use.
func TestBuildOrchestratorMCPConfigSkipsEveryEnvWithoutAnExecutable(t *testing.T) {
	config, _, _ := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "  ", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)
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

// TestOrchestratorMCPUnwiredActionNamesTheControl is the red-then-green
// regression for the "Install the erun command line tool, then restart the
// orchestrator" dead end: neither half was ever something the desktop could
// perform, so the action a caller attaches to the notice must let the
// frontend link the install docs and drive the restart directly.
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
	_, _, unreachable := buildOrchestratorMCPConfig(orchestratorTestEnvs(), "/opt/erun/bin/erun", orchestratorTestPort, orchestratorTestNoHostEnvType, orchestratorTestAlwaysReachable)
	if len(unreachable) != 0 {
		t.Fatalf("expected no unreachable envs when every edge answers, got %+v", unreachable)
	}
}

// orchestratorTestPortWithHost resolves a real port for the host fixture's
// tenant and delegates every other tenant to the shared stub. It exists because
// the shared stub answers 0 for a tenant it does not know, which would let the
// host regression below pass for the wrong reason: an env dropped by the port
// guard produces exactly the same output as one dropped by the type check, so a
// builder that consulted the port BEFORE the type would still satisfy every
// assertion. A host env that resolves a port is the production case too -- port
// allocation is type-blind and happily allocates one.
func orchestratorTestPortWithHost(tenant, environment string) int {
	if tenant == "frs" {
		return 17100
	}
	return orchestratorTestPort(tenant, environment)
}

// hostAndOrdinaryEnvs is the fixture for the host tests below: one host tenant
// and two ordinary ones, every one of which resolves an MCP port through
// orchestratorTestPortWithHost.
func hostAndOrdinaryEnvs() []eruncommon.OrchestratorEnvConfig {
	return []eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "host"},
		{Tenant: "petios", Environment: "rihards-win-develop"},
		{Tenant: "erun", Environment: "main"},
	}
}

// TestBuildOrchestratorMCPConfigSkipsHostEnvBeforeThePortLookup is the
// regression for the defect this change exists to fix: a host environment was
// wired as an MCP edge that can never answer. A host env has no pod, so no
// erun MCP edge runs for it and no port-forward can ever reach one -- but the
// port resolver is type-blind and happily allocates it one, so the env was
// wired and then reported as "its edge is not answering", prescribing a
// deploy or reopen that a host environment refuses.
//
// The ordering the name claims is pinned by the port resolver, not by the
// assertions alone: orchestratorTestPortWithHost gives frs/host a port that
// resolves, so deleting the type check above the port lookup wires the host env
// and fails the assertions below.
func TestBuildOrchestratorMCPConfigSkipsHostEnvBeforeThePortLookup(t *testing.T) {
	config, _, unreachable := buildOrchestratorMCPConfig(hostAndOrdinaryEnvs(), "/opt/erun/bin/erun", orchestratorTestPortWithHost, orchestratorTestEnvTypeHosts("frs"), orchestratorTestAlwaysReachable)

	if _, ok := config.MCPServers["frs-host"]; ok {
		t.Fatalf("a host env must not be wired as an MCP edge: %v", config.MCPServers)
	}
	if len(config.MCPServers) != 2 {
		t.Fatalf("expected the two ordinary envs still wired, got %d: %v", len(config.MCPServers), config.MCPServers)
	}
	if _, ok := config.MCPServers["petios-rihards-win-develop"]; !ok {
		t.Fatalf("expected the ordinary envs unaffected by the host skip: %v", config.MCPServers)
	}
	// Not merely unwired -- never probed. Reporting it unreachable would attach
	// the deploy-or-reopen remedy to an env that refuses deploy.
	for _, env := range unreachable {
		if env.Label == "frs/host" {
			t.Fatalf("a host env must not be reported as an unreachable edge: %+v", unreachable)
		}
	}
}

// TestBuildOrchestratorMCPConfigReportsAHostSkipWithoutCallingItAProblem covers
// the other half of the same wiring decision, which the test above deliberately
// does not: the two notices an orchestrator launch shows are different, and a
// host env belongs in the one explaining it has no edge at all rather than the
// one that lists a wiring problem and prescribes a remedy the host env refuses.
func TestBuildOrchestratorMCPConfigReportsAHostSkipWithoutCallingItAProblem(t *testing.T) {
	_, skipped, _ := buildOrchestratorMCPConfig(hostAndOrdinaryEnvs(), "/opt/erun/bin/erun", orchestratorTestPortWithHost, orchestratorTestEnvTypeHosts("frs"), orchestratorTestAlwaysReachable)

	hostEnvs, problems := splitOrchestratorMCPHostSkips(skipped)
	if len(hostEnvs) != 1 {
		t.Fatalf("expected the host env reported as a host skip, got %+v (problems: %+v)", skipped, problems)
	}
	if hostEnvs[0].Label != "frs/host" {
		t.Fatalf("host skip label = %q, want frs/host", hostEnvs[0].Label)
	}
	if len(problems) != 0 {
		t.Fatalf("a host env must not be reported as a wiring problem: %+v", problems)
	}
	for _, reason := range []string{"no pod", "MCP edge"} {
		if !strings.Contains(hostEnvs[0].Reason, reason) {
			t.Errorf("host skip reason %q does not name %q", hostEnvs[0].Reason, reason)
		}
	}
}

// TestOrchestratorMCPOnlyHostSkips locks the distinction the unwired-error
// path turns on: an orchestrator whose every linked environment is a host
// environment is a WORKING configuration, so it must not take the "no linked
// environment resolved an MCP port" error path meant for a real resolution
// failure. A single non-host skip anywhere in the list is enough to make it a
// real failure again.
func TestOrchestratorMCPOnlyHostSkips(t *testing.T) {
	host := orchestratorMCPSkip{Label: "frs/host", Reason: "it is a host environment", HostEnv: true}
	problem := orchestratorMCPSkip{Label: "ghost/x", Reason: "it resolved no MCP port"}

	cases := []struct {
		name    string
		skipped []orchestratorMCPSkip
		want    bool
	}{
		{name: "nothing skipped at all", skipped: nil, want: false},
		{name: "one host env", skipped: []orchestratorMCPSkip{host}, want: true},
		{name: "two host envs", skipped: []orchestratorMCPSkip{host, host}, want: true},
		{name: "one real problem", skipped: []orchestratorMCPSkip{problem}, want: false},
		{name: "host envs alongside a real problem", skipped: []orchestratorMCPSkip{host, problem}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := orchestratorMCPOnlyHostSkips(tc.skipped); got != tc.want {
				t.Fatalf("orchestratorMCPOnlyHostSkips(%+v) = %v, want %v", tc.skipped, got, tc.want)
			}
		})
	}
}

// TestSplitOrchestratorMCPHostSkips locks the split the two notices depend on:
// the host envs must be lifted out of the problem list, in order, with each
// side keeping every entry that belongs to it -- dropping one would reproduce
// the silent-drop bug this reporting exists to prevent, just on the other
// channel.
func TestSplitOrchestratorMCPHostSkips(t *testing.T) {
	hostA := orchestratorMCPSkip{Label: "frs/host", HostEnv: true}
	hostB := orchestratorMCPSkip{Label: "frs/laptop", HostEnv: true}
	problemA := orchestratorMCPSkip{Label: "ghost/one"}
	problemB := orchestratorMCPSkip{Label: "?/z"}

	hostEnvs, problems := splitOrchestratorMCPHostSkips([]orchestratorMCPSkip{problemA, hostA, problemB, hostB})
	if len(hostEnvs) != 2 || hostEnvs[0].Label != "frs/host" || hostEnvs[1].Label != "frs/laptop" {
		t.Fatalf("host envs = %+v, want frs/host then frs/laptop in order", hostEnvs)
	}
	if len(problems) != 2 || problems[0].Label != "ghost/one" || problems[1].Label != "?/z" {
		t.Fatalf("problems = %+v, want ghost/one then ?/z in order", problems)
	}

	hostEnvs, problems = splitOrchestratorMCPHostSkips(nil)
	if len(hostEnvs) != 0 || len(problems) != 0 {
		t.Fatalf("splitting nothing must yield nothing, got host=%+v problems=%+v", hostEnvs, problems)
	}
}

// TestOrchestratorMCPHostEnvNoticeNamesTheEnvironmentsAndTheRecovery: the
// notice is the only thing that tells an operator a linked environment is
// absent from the session's toolset on purpose rather than by a wiring
// failure, so it has to name which one and what to do instead -- "some
// environments have no tools" is not actionable, and neither is the partial
// notice's "check those environments still exist, then restart", which
// prescribes a restart that changes nothing for a host env.
func TestOrchestratorMCPHostEnvNoticeNamesTheEnvironmentsAndTheRecovery(t *testing.T) {
	notice := orchestratorMCPHostEnvNotice("erun-issues", []orchestratorMCPSkip{
		{Label: "frs/host", HostEnv: true},
		{Label: "frs/laptop", HostEnv: true},
	})
	for _, want := range []string{"erun-issues", "frs/host", "frs/laptop", "no pod", "MCP edge", "directory"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "restart") {
		t.Errorf("notice must not prescribe a restart, which fixes nothing for a host env:\n%s", notice)
	}

	// An unnamed orchestrator still gets a readable line rather than one opening
	// on a blank label.
	if unnamed := orchestratorMCPHostEnvNotice("  ", []orchestratorMCPSkip{{Label: "frs/host", HostEnv: true}}); !strings.HasPrefix(unnamed, "The orchestrator ") {
		t.Fatalf("unnamed notice = %q, want it to fall back to a generic label", unnamed)
	}
}

// orchestratorTestAppWithHostEnv is orchestratorTestApp with one extra
// environment staged in the store: frs/host, a real host env. Built on top of
// the shared stub rather than added to newOrchestratorStubStore so the host
// env exists only for the tests that are about it -- every other orchestrator
// test keeps the exact env set it was written against.
func orchestratorTestAppWithHostEnv(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	store := newOrchestratorStubStore(t.TempDir())
	store.envs["frs/host"] = eruncommon.EnvConfig{
		Name:          "host",
		Type:          eruncommon.EnvironmentTypeHost,
		LocalRepoPath: t.TempDir(),
	}
	app := NewApp(erunUIDeps{
		store: store,
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		canReachMCPEndpoint: orchestratorTestAlwaysReachable,
	})
	app.investigations.reportDir = t.TempDir()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

// TestWriteOrchestratorMCPConfigHostOnlyIsAWorkingConfiguration drives the
// full write path against a real store, because the defect was never in the
// builder alone: writeOrchestratorMCPConfig turns "nothing wired" into
// errOrchestratorMCPNoPort, and an orchestrator whose only linked environment
// is a host environment would have hit exactly that path -- reporting a
// working link as a failure to resolve a port. It must return no error, and
// still carry the skip so the notice can be rendered.
func TestWriteOrchestratorMCPConfigHostOnlyIsAWorkingConfiguration(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app := orchestratorTestAppWithHostEnv(t)

	path, skipped, _, err := app.writeOrchestratorMCPConfig("host-only", []eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "host"},
	})
	if err != nil {
		t.Fatalf("a host-only orchestrator must not be an error, got %v", err)
	}
	if strings.TrimSpace(path) != "" {
		t.Errorf("path = %q, want empty -- there is no MCP edge to configure", path)
	}
	if len(skipped) != 1 {
		t.Fatalf("reported %d skips, want 1 so the notice can name the env: %+v", len(skipped), skipped)
	}
	if !skipped[0].HostEnv || skipped[0].Label != "frs/host" {
		t.Fatalf("skip = %+v, want a host-flagged skip labelled frs/host", skipped[0])
	}
}

// TestWireOrchestratorMCPHostEnvGetsAnInfoNoticeNotAWarning is the end-to-end
// regression for the operator-facing half: a host-only orchestrator launches
// with no MCP config at all, and the only thing distinguishing that working
// state from a broken link is the notice. It must be informational, name the
// environment, and never carry the warning the partial notice uses -- a
// warning banner here would send an operator to fix a configuration that is
// already correct.
func TestWireOrchestratorMCPHostEnvGetsAnInfoNoticeNotAWarning(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app := orchestratorTestAppWithHostEnv(t)
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	path := app.wireOrchestratorMCP("petios", "Petios", []eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "host"},
	})
	if strings.TrimSpace(path) != "" {
		t.Fatalf("path = %q, want empty for a host-only orchestrator", path)
	}

	events := emits.events(appNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected exactly one notice for the host env, got %+v", events)
	}
	payload, ok := events[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if payload.Kind != "info" {
		t.Fatalf("kind = %q, want info -- a host env has no edge by design, so this is not a warning", payload.Kind)
	}
	for _, want := range []string{"Petios", "frs/host", "no pod"} {
		if !strings.Contains(payload.Message, want) {
			t.Errorf("notice does not mention %q: %q", want, payload.Message)
		}
	}
}

// TestWireOrchestratorMCPHostEnvIsNotCountedAsMissing locks the arithmetic the
// partial notice rests on. A host env has no MCP edge to lose, so counting it
// as "missing" would report a healthy orchestrator as one that started with
// tools for fewer environments than it links. With a working env alongside it,
// nothing is missing at all and the partial warning must not fire.
func TestWireOrchestratorMCPHostEnvIsNotCountedAsMissing(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app := orchestratorTestAppWithHostEnv(t)
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	path := app.wireOrchestratorMCP("petios", "Petios", []eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "host"},
		{Tenant: "frs", Environment: "dev"},
	})
	if strings.TrimSpace(path) == "" {
		t.Fatal("expected the ordinary env still wired alongside the host env")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "frs-dev") {
		t.Fatalf("expected the ordinary env wired:\n%s", data)
	}
	if strings.Contains(string(data), "frs-host") {
		t.Fatalf("a host env must not appear in the written config:\n%s", data)
	}

	for _, event := range emits.events(appNotificationEvent) {
		payload, ok := event.(appNotificationPayload)
		if !ok {
			t.Fatalf("unexpected payload type: %T", event)
		}
		if payload.Kind == "warning" {
			t.Fatalf("a working orchestrator must carry no warning: %q", payload.Message)
		}
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
	notice := orchestratorMCPPartialNotice("erun-issues", 1, 2, nil, []orchestratorMCPSkip{
		{Label: "petios/rihards-review", Reason: "it resolved no MCP port"},
	})

	for _, want := range []string{"erun-issues", "1 of 2", "petios/rihards-review", "resolved no MCP port", "restart"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}
	// No host env linked means no host aside: this is the message an
	// orchestrator without one has always shown.
	if strings.Contains(notice, "no MCP edge is expected") {
		t.Errorf("notice mentions a host env when none is linked:\n%s", notice)
	}
}

// TestOrchestratorMCPPartialNoticeCountsHostEnvsInItsDenominator covers the
// three-env case the exclusion used to get wrong. A host env has no MCP edge to
// wire, so it must not appear among the problems -- but it IS one of the linked
// environments, so a denominator that omitted it described a smaller
// orchestrator than the one the operator linked, and an operator counting their
// own entries read the notice as arithmetic that did not add up.
func TestOrchestratorMCPPartialNoticeCountsHostEnvsInItsDenominator(t *testing.T) {
	notice := orchestratorMCPPartialNotice("erun-issues", 1, 3,
		[]orchestratorMCPSkip{{Label: "frs/host", Reason: "it is a host environment, which has no pod and so no MCP edge to reach", HostEnv: true}},
		[]orchestratorMCPSkip{{Label: "petios/rihards-review", Reason: "it resolved no MCP port"}},
	)

	// Three linked, one wired: the count must say so.
	for _, want := range []string{"1 of 3", "frs/host", "no MCP edge is expected"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice does not mention %q:\n%s", want, notice)
		}
	}
	// The host env is named as the reason for the shortfall, never as a problem:
	// the remedy beside this notice is a restart, which a host env does not need.
	if strings.Contains(notice, "frs/host (") {
		t.Errorf("notice lists the host env among the missing problems:\n%s", notice)
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

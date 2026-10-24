package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	orchestratorpkg "github.com/sophium/erun/erun-common/command/orchestrator"
)

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
func hostAndOrdinaryEnvs() []configpkg.OrchestratorEnvConfig {
	return []configpkg.OrchestratorEnvConfig{
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
	store.envs["frs/host"] = configpkg.EnvConfig{
		Name:          "host",
		Type:          configpkg.EnvironmentTypeHost,
		LocalRepoPath: t.TempDir(),
	}
	app := NewApp(erunUIDeps{
		store: store,
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchestratorpkg.OrchestratorLaunch, string, string, string) (string, []string, error) {
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

	path, skipped, _, err := app.writeOrchestratorMCPConfig("host-only", []configpkg.OrchestratorEnvConfig{
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

	path := app.wireOrchestratorMCP("petios", "Petios", []configpkg.OrchestratorEnvConfig{
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

	path := app.wireOrchestratorMCP("petios", "Petios", []configpkg.OrchestratorEnvConfig{
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
